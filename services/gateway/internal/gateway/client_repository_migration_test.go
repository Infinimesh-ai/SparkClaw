package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type clientRepositoryFaultStore struct {
	Repository

	getClientFn    func(context.Context, string) (app.Client, bool, error)
	listClientsFn  func(context.Context) ([]app.Client, error)
	revokeClientFn func(context.Context, string) (app.Client, error)
	findClientFn   func(context.Context, string) (app.Client, bool, error)
	touchClientFn  func(context.Context, string) (app.Client, bool, error)
	savePairingFn  func(context.Context, app.PairingCode) (app.PairingCode, error)
	getPairingFn   func(context.Context, string) (app.PairingCode, bool, error)
	claimPairingFn func(context.Context, string, app.Client) (app.PairingCode, app.Client, error)
}

func (s *clientRepositoryFaultStore) GetClient(ctx context.Context, id string) (app.Client, bool, error) {
	if s.getClientFn != nil {
		return s.getClientFn(ctx, id)
	}
	return s.Repository.GetClient(ctx, id)
}

func (s *clientRepositoryFaultStore) ListClients(ctx context.Context) ([]app.Client, error) {
	if s.listClientsFn != nil {
		return s.listClientsFn(ctx)
	}
	return s.Repository.ListClients(ctx)
}

func (s *clientRepositoryFaultStore) RevokeClient(ctx context.Context, id string) (app.Client, error) {
	if s.revokeClientFn != nil {
		return s.revokeClientFn(ctx, id)
	}
	return s.Repository.RevokeClient(ctx, id)
}

func (s *clientRepositoryFaultStore) FindClientByTokenHash(ctx context.Context, tokenHash string) (app.Client, bool, error) {
	if s.findClientFn != nil {
		return s.findClientFn(ctx, tokenHash)
	}
	return s.Repository.FindClientByTokenHash(ctx, tokenHash)
}

func (s *clientRepositoryFaultStore) TouchClient(ctx context.Context, id string) (app.Client, bool, error) {
	if s.touchClientFn != nil {
		return s.touchClientFn(ctx, id)
	}
	return s.Repository.TouchClient(ctx, id)
}

func (s *clientRepositoryFaultStore) SavePairingCode(ctx context.Context, code app.PairingCode) (app.PairingCode, error) {
	if s.savePairingFn != nil {
		return s.savePairingFn(ctx, code)
	}
	return s.Repository.SavePairingCode(ctx, code)
}

func (s *clientRepositoryFaultStore) GetPairingCode(ctx context.Context, id string) (app.PairingCode, bool, error) {
	if s.getPairingFn != nil {
		return s.getPairingFn(ctx, id)
	}
	return s.Repository.GetPairingCode(ctx, id)
}

func (s *clientRepositoryFaultStore) ClaimPairingCode(ctx context.Context, id string, client app.Client) (app.PairingCode, app.Client, error) {
	if s.claimPairingFn != nil {
		return s.claimPairingFn(ctx, id, client)
	}
	return s.Repository.ClaimPairingCode(ctx, id, client)
}

func newClientRepositoryTestServer(t *testing.T, repository Repository) *Server {
	t.Helper()
	cfg := config.Default()
	cfg.Gateway.PairingRequired = true
	lifecycle, cancel := context.WithCancel(context.Background())
	server := &Server{
		cfg:          cfg,
		store:        repository,
		pairing:      newPairingCoordinator(),
		lifecycleCtx: lifecycle,
	}
	t.Cleanup(func() {
		if err := server.pairing.gate.Acquire(context.Background(), 1); err == nil {
			server.clearPairingPendingLocked()
			server.pairing.gate.Release(1)
		}
		cancel()
	})
	return server
}

func clientRepositoryRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:44000"
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}

func clientRepositoryRequestAsOwner(request *http.Request, ownerID string) *http.Request {
	principal := requestPrincipal{OwnerID: ownerID, ActorID: ownerID}
	return request.WithContext(context.WithValue(request.Context(), requestPrincipalContextKey{}, principal))
}

func invokePairingStart(server *Server, request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	server.startPairing(response, request)
	return response
}

func invokePairingClaim(server *Server, body string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	server.claimPairing(response, clientRepositoryRequest(http.MethodPost, "/api/pairing/claim", body))
	return response
}

func assertClientRepositoryError(t *testing.T, response *httptest.ResponseRecorder, status int, message string, private ...string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status=%d body=%s, want %d", response.Code, response.Body.String(), status)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error response: %v body=%s", err, response.Body.String())
	}
	if payload["error"] != message {
		t.Fatalf("error=%v, want %q; body=%s", payload["error"], message, response.Body.String())
	}
	for _, forbidden := range private {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, response.Body.String())
		}
	}
}

func decodePairingStart(t *testing.T, response *httptest.ResponseRecorder) (string, string, time.Time) {
	t.Helper()
	var payload struct {
		PairingID string    `json:"pairing_id"`
		Code      string    `json:"code"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode pairing start: %v body=%s", err, response.Body.String())
	}
	return payload.PairingID, payload.Code, payload.ExpiresAt
}

func decodePairingClaim(t *testing.T, response *httptest.ResponseRecorder) (app.Client, string) {
	t.Helper()
	var payload struct {
		Client app.Client `json:"client"`
		Token  string     `json:"token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode pairing claim: %v body=%s", err, response.Body.String())
	}
	return payload.Client, payload.Token
}

func clientRepositoryStoreError(code store.StoreErrorCode, operation store.StoreOperation) error {
	return &store.StoreError{
		Code:      code,
		Operation: operation,
		Err:       errors.New("postgres://owner:secret@database/private-path"),
	}
}

func saveClientRepositoryPairing(t *testing.T, repository store.ClientRepository, id, plaintext string, expiresAt time.Time) app.PairingCode {
	t.Helper()
	pairing, err := repository.SavePairingCode(t.Context(), app.PairingCode{
		ID:        id,
		CodeHash:  hashSecret(plaintext),
		Status:    "pending",
		ExpiresAt: expiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return pairing
}

func TestPairingStartUnknownCandidateReconciliation(t *testing.T) {
	t.Run("immediate exact candidate", func(t *testing.T) {
		base := store.NewMemoryStore()
		saveCalls := 0
		getCalls := 0
		faults := &clientRepositoryFaultStore{Repository: base}
		faults.savePairingFn = func(ctx context.Context, attempted app.PairingCode) (app.PairingCode, error) {
			saveCalls++
			saved, err := base.SavePairingCode(ctx, attempted)
			if err != nil {
				return app.PairingCode{}, err
			}
			return saved, clientRepositoryStoreError(store.StoreErrorUnknownOutcome, store.OperationPairingCodeSave)
		}
		faults.getPairingFn = func(ctx context.Context, id string) (app.PairingCode, bool, error) {
			getCalls++
			return base.GetPairingCode(ctx, id)
		}
		server := newClientRepositoryTestServer(t, faults)

		response := invokePairingStart(server, clientRepositoryRequest(http.MethodPost, "/api/pairing/start", ""))
		if response.Code != http.StatusCreated {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		pairingID, code, expiresAt := decodePairingStart(t, response)
		if pairingID == "" || code == "" || expiresAt.IsZero() || saveCalls != 1 || getCalls != 1 || server.pairing.pending != nil {
			t.Fatalf("pairing=%q code=%q expiry=%v saves=%d gets=%d pending=%#v", pairingID, code, expiresAt, saveCalls, getCalls, server.pairing.pending)
		}
	})

	t.Run("next request exact candidate", func(t *testing.T) {
		base := store.NewMemoryStore()
		saveCalls := 0
		getCalls := 0
		faults := &clientRepositoryFaultStore{Repository: base}
		faults.savePairingFn = func(ctx context.Context, attempted app.PairingCode) (app.PairingCode, error) {
			saveCalls++
			saved, err := base.SavePairingCode(ctx, attempted)
			if err != nil {
				return app.PairingCode{}, err
			}
			return saved, clientRepositoryStoreError(store.StoreErrorUnknownOutcome, store.OperationPairingCodeSave)
		}
		faults.getPairingFn = func(ctx context.Context, id string) (app.PairingCode, bool, error) {
			getCalls++
			if getCalls == 1 {
				return app.PairingCode{}, false, clientRepositoryStoreError(store.StoreErrorUnavailable, store.OperationPairingCodeGet)
			}
			return base.GetPairingCode(ctx, id)
		}
		server := newClientRepositoryTestServer(t, faults)

		first := invokePairingStart(server, clientRepositoryRequest(http.MethodPost, "/api/pairing/start", ""))
		assertClientRepositoryError(t, first, http.StatusServiceUnavailable, "pairing is temporarily unavailable", "secret", "private-path")
		if server.pairing.pending == nil {
			t.Fatal("unknown reconciliation error cleared pending start")
		}
		originalCode := server.pairing.pending.plaintextCode
		originalID := server.pairing.pending.attemptedPairing.ID
		second := invokePairingStart(server, clientRepositoryRequest(http.MethodPost, "/api/pairing/start", ""))
		if second.Code != http.StatusCreated {
			t.Fatalf("status=%d body=%s", second.Code, second.Body.String())
		}
		pairingID, code, _ := decodePairingStart(t, second)
		if pairingID != originalID || code != originalCode || saveCalls != 1 || getCalls != 2 {
			t.Fatalf("pairing=%q/%q code=%q/%q saves=%d gets=%d", pairingID, originalID, code, originalCode, saveCalls, getCalls)
		}
	})
}

func TestPairingStartZeroCandidateBarriers(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		present    bool
		wantStatus int
		wantError  string
	}{
		{name: "absence proves rollback", wantStatus: http.StatusServiceUnavailable, wantError: "pairing is temporarily unavailable"},
		{name: "present conflicts", present: true, wantStatus: http.StatusConflict, wantError: "pairing state changed"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			base := store.NewMemoryStore()
			var attempted app.PairingCode
			faults := &clientRepositoryFaultStore{Repository: base}
			faults.savePairingFn = func(_ context.Context, code app.PairingCode) (app.PairingCode, error) {
				attempted = code
				return app.PairingCode{}, clientRepositoryStoreError(store.StoreErrorUnknownOutcome, store.OperationPairingCodeSave)
			}
			faults.getPairingFn = func(_ context.Context, id string) (app.PairingCode, bool, error) {
				if id != attempted.ID || id == "" {
					t.Fatalf("barrier ID=%q attempted=%q", id, attempted.ID)
				}
				if !testCase.present {
					return app.PairingCode{}, false, nil
				}
				present := attempted
				present.CodeHash = "different-code-hash"
				present.CreatedAt = time.Now().UTC()
				return present, true, nil
			}
			server := newClientRepositoryTestServer(t, faults)

			response := invokePairingStart(server, clientRepositoryRequest(http.MethodPost, "/api/pairing/start", ""))
			assertClientRepositoryError(t, response, testCase.wantStatus, testCase.wantError)
			if attempted.ID == "" || attempted.CodeHash == "" || server.pairing.pending != nil || strings.Contains(response.Body.String(), `"code"`) {
				t.Fatalf("attempted=%#v pending=%#v body=%s", attempted, server.pairing.pending, response.Body.String())
			}
		})
	}
}

func TestPairingStartPendingDoesNotRegenerateAndDifferentOwnerConflicts(t *testing.T) {
	base := store.NewMemoryStore()
	saveCalls := 0
	getCalls := 0
	faults := &clientRepositoryFaultStore{Repository: base}
	faults.savePairingFn = func(ctx context.Context, attempted app.PairingCode) (app.PairingCode, error) {
		saveCalls++
		saved, err := base.SavePairingCode(ctx, attempted)
		if err != nil {
			return app.PairingCode{}, err
		}
		return saved, clientRepositoryStoreError(store.StoreErrorUnknownOutcome, store.OperationPairingCodeSave)
	}
	faults.getPairingFn = func(context.Context, string) (app.PairingCode, bool, error) {
		getCalls++
		return app.PairingCode{}, false, clientRepositoryStoreError(store.StoreErrorUnavailable, store.OperationPairingCodeGet)
	}
	server := newClientRepositoryTestServer(t, faults)

	first := invokePairingStart(server, clientRepositoryRequest(http.MethodPost, "/api/pairing/start", ""))
	assertClientRepositoryError(t, first, http.StatusServiceUnavailable, "pairing is temporarily unavailable")
	pending := server.pairing.pending
	if pending == nil {
		t.Fatal("first unresolved start did not retain pending state")
	}
	originalGeneration := pending.generation
	originalID := pending.attemptedPairing.ID
	originalHash := pending.attemptedPairing.CodeHash
	originalCode := pending.plaintextCode

	second := invokePairingStart(server, clientRepositoryRequest(http.MethodPost, "/api/pairing/start", ""))
	assertClientRepositoryError(t, second, http.StatusServiceUnavailable, "pairing is temporarily unavailable")
	if server.pairing.pending != pending || pending.generation != originalGeneration || pending.attemptedPairing.ID != originalID ||
		pending.attemptedPairing.CodeHash != originalHash || pending.plaintextCode != originalCode || saveCalls != 1 || getCalls != 2 {
		t.Fatalf("pending regenerated: pending=%#v saves=%d gets=%d", pending, saveCalls, getCalls)
	}

	differentOwner := clientRepositoryRequestAsOwner(clientRepositoryRequest(http.MethodPost, "/api/pairing/start", ""), "owner-other")
	conflict := invokePairingStart(server, differentOwner)
	assertClientRepositoryError(t, conflict, http.StatusConflict, "another pairing request is pending")
	if saveCalls != 1 || getCalls != 2 || server.pairing.pending != pending {
		t.Fatalf("different owner changed pending command: saves=%d gets=%d pending=%#v", saveCalls, getCalls, server.pairing.pending)
	}
}

func TestPairingClaimUnknownCandidateReconciliation(t *testing.T) {
	for _, testCase := range []struct {
		name              string
		failImmediateRead bool
	}{
		{name: "immediate exact candidate"},
		{name: "next request token recovery", failImmediateRead: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			base := store.NewMemoryStore()
			plaintextCode := "claim-code"
			pairing := saveClientRepositoryPairing(t, base, "pair-claim", plaintextCode, time.Now().UTC().Add(time.Hour))
			claimCalls := 0
			postClaimReads := 0
			claimCommitted := false
			faults := &clientRepositoryFaultStore{Repository: base}
			faults.claimPairingFn = func(ctx context.Context, id string, client app.Client) (app.PairingCode, app.Client, error) {
				claimCalls++
				claimedPairing, claimedClient, err := base.ClaimPairingCode(ctx, id, client)
				if err != nil {
					return app.PairingCode{}, app.Client{}, err
				}
				claimCommitted = true
				return claimedPairing, claimedClient, clientRepositoryStoreError(store.StoreErrorUnknownOutcome, store.OperationPairingCodeClaim)
			}
			faults.getPairingFn = func(ctx context.Context, id string) (app.PairingCode, bool, error) {
				if claimCommitted {
					postClaimReads++
					if testCase.failImmediateRead && postClaimReads == 1 {
						return app.PairingCode{}, false, clientRepositoryStoreError(store.StoreErrorUnavailable, store.OperationPairingCodeGet)
					}
				}
				return base.GetPairingCode(ctx, id)
			}
			server := newClientRepositoryTestServer(t, faults)
			body := `{"pairing_id":"` + pairing.ID + `","code":"` + plaintextCode + `","client_name":"webchat"}`

			first := invokePairingClaim(server, body)
			if testCase.failImmediateRead {
				assertClientRepositoryError(t, first, http.StatusServiceUnavailable, "pairing is temporarily unavailable")
				if server.pairing.pending == nil {
					t.Fatal("unresolved claim did not retain pending token")
				}
				originalToken := server.pairing.pending.plaintextToken
				second := invokePairingClaim(server, body)
				if second.Code != http.StatusCreated {
					t.Fatalf("status=%d body=%s", second.Code, second.Body.String())
				}
				_, recoveredToken := decodePairingClaim(t, second)
				if recoveredToken == "" || recoveredToken != originalToken {
					t.Fatalf("recovered token=%q original=%q", recoveredToken, originalToken)
				}
			} else {
				if first.Code != http.StatusCreated {
					t.Fatalf("status=%d body=%s", first.Code, first.Body.String())
				}
				client, token := decodePairingClaim(t, first)
				if client.ID == "" || token == "" {
					t.Fatalf("client=%#v token=%q", client, token)
				}
			}
			if claimCalls != 1 || server.pairing.pending != nil {
				t.Fatalf("claim calls=%d pending=%#v", claimCalls, server.pairing.pending)
			}
		})
	}
}

func TestPairingClaimPendingValidatesCodeAndFingerprintBeforeRecovery(t *testing.T) {
	base := store.NewMemoryStore()
	plaintextCode := "retained-code"
	pairing := saveClientRepositoryPairing(t, base, "pair-retained", plaintextCode, time.Now().UTC().Add(time.Hour))
	claimCalls := 0
	getCalls := 0
	claimCommitted := false
	faults := &clientRepositoryFaultStore{Repository: base}
	faults.claimPairingFn = func(ctx context.Context, id string, client app.Client) (app.PairingCode, app.Client, error) {
		claimCalls++
		claimedPairing, claimedClient, err := base.ClaimPairingCode(ctx, id, client)
		if err != nil {
			return app.PairingCode{}, app.Client{}, err
		}
		claimCommitted = true
		return claimedPairing, claimedClient, clientRepositoryStoreError(store.StoreErrorUnknownOutcome, store.OperationPairingCodeClaim)
	}
	faults.getPairingFn = func(ctx context.Context, id string) (app.PairingCode, bool, error) {
		if claimCommitted {
			getCalls++
			return app.PairingCode{}, false, clientRepositoryStoreError(store.StoreErrorUnavailable, store.OperationPairingCodeGet)
		}
		return base.GetPairingCode(ctx, id)
	}
	server := newClientRepositoryTestServer(t, faults)
	body := `{"pairing_id":"` + pairing.ID + `","code":"` + plaintextCode + `","client_name":"first"}`
	first := invokePairingClaim(server, body)
	assertClientRepositoryError(t, first, http.StatusServiceUnavailable, "pairing is temporarily unavailable")
	pending := server.pairing.pending
	if pending == nil {
		t.Fatal("unresolved claim did not retain pending state")
	}

	invalid := invokePairingClaim(server, `{"pairing_id":"`+pairing.ID+`","code":"wrong","client_name":"first"}`)
	assertClientRepositoryError(t, invalid, http.StatusUnauthorized, "invalid pairing code")
	if server.pairing.pending != pending || claimCalls != 1 || getCalls != 1 {
		t.Fatalf("invalid retry touched recovery: claims=%d gets=%d pending=%#v", claimCalls, getCalls, server.pairing.pending)
	}

	differentValid := invokePairingClaim(server, `{"pairing_id":"`+pairing.ID+`","code":"`+plaintextCode+`","client_name":"different"}`)
	assertClientRepositoryError(t, differentValid, http.StatusConflict, "another pairing request is pending")
	if server.pairing.pending != pending || claimCalls != 1 || getCalls != 1 {
		t.Fatalf("different valid retry touched recovery: claims=%d gets=%d pending=%#v", claimCalls, getCalls, server.pairing.pending)
	}
}

func TestPairingClaimZeroCandidateRollbackBarrier(t *testing.T) {
	base := store.NewMemoryStore()
	plaintextCode := "rollback-code"
	pairing := saveClientRepositoryPairing(t, base, "pair-rollback", plaintextCode, time.Now().UTC().Add(time.Hour))
	var attemptedClient app.Client
	getClientCalls := 0
	faults := &clientRepositoryFaultStore{Repository: base}
	faults.claimPairingFn = func(_ context.Context, id string, client app.Client) (app.PairingCode, app.Client, error) {
		if id != pairing.ID {
			t.Fatalf("claim pairing ID=%q, want %q", id, pairing.ID)
		}
		attemptedClient = client
		return app.PairingCode{}, app.Client{}, clientRepositoryStoreError(store.StoreErrorUnknownOutcome, store.OperationPairingCodeClaim)
	}
	faults.getClientFn = func(ctx context.Context, id string) (app.Client, bool, error) {
		getClientCalls++
		if id == "" || id != attemptedClient.ID {
			t.Fatalf("client barrier ID=%q attempted=%q", id, attemptedClient.ID)
		}
		return base.GetClient(ctx, id)
	}
	server := newClientRepositoryTestServer(t, faults)

	response := invokePairingClaim(server, `{"pairing_id":"`+pairing.ID+`","code":"`+plaintextCode+`","client_name":"rollback"}`)
	assertClientRepositoryError(t, response, http.StatusServiceUnavailable, "pairing is temporarily unavailable")
	persistedPairing, found, err := base.GetPairingCode(t.Context(), pairing.ID)
	if err != nil || !found || !store.PairingCodesEqual(persistedPairing, pairing) || attemptedClient.ID == "" ||
		attemptedClient.TokenHash == "" || getClientCalls != 1 || server.pairing.pending != nil || strings.Contains(response.Body.String(), `"token"`) {
		t.Fatalf("pairing=%#v found=%v err=%v attempted=%#v gets=%d pending=%#v body=%s", persistedPairing, found, err, attemptedClient, getClientCalls, server.pairing.pending, response.Body.String())
	}
}

func TestPairingCompletionExpiryNeverDisclosesSecret(t *testing.T) {
	t.Run("start", func(t *testing.T) {
		base := store.NewMemoryStore()
		faults := &clientRepositoryFaultStore{Repository: base}
		server := newClientRepositoryTestServer(t, faults)
		fakeNow := time.Now().UTC()
		server.pairing.now = func() time.Time { return fakeNow }
		faults.savePairingFn = func(ctx context.Context, attempted app.PairingCode) (app.PairingCode, error) {
			saved, err := base.SavePairingCode(ctx, attempted)
			fakeNow = saved.ExpiresAt
			return saved, err
		}

		response := invokePairingStart(server, clientRepositoryRequest(http.MethodPost, "/api/pairing/start", ""))
		assertClientRepositoryError(t, response, http.StatusServiceUnavailable, "pairing is temporarily unavailable")
		if strings.Contains(response.Body.String(), `"code"`) || server.pairing.pending != nil {
			t.Fatalf("expired start disclosed or retained secret: body=%s pending=%#v", response.Body.String(), server.pairing.pending)
		}
	})

	t.Run("claim", func(t *testing.T) {
		base := store.NewMemoryStore()
		plaintextCode := "expiry-code"
		fakeNow := time.Now().UTC()
		pairing := saveClientRepositoryPairing(t, base, "pair-expiry", plaintextCode, fakeNow.Add(time.Hour))
		faults := &clientRepositoryFaultStore{Repository: base}
		server := newClientRepositoryTestServer(t, faults)
		server.pairing.now = func() time.Time { return fakeNow }
		faults.claimPairingFn = func(ctx context.Context, id string, client app.Client) (app.PairingCode, app.Client, error) {
			claimedPairing, claimedClient, err := base.ClaimPairingCode(ctx, id, client)
			fakeNow = claimedPairing.ExpiresAt
			return claimedPairing, claimedClient, err
		}

		response := invokePairingClaim(server, `{"pairing_id":"`+pairing.ID+`","code":"`+plaintextCode+`","client_name":"expiry"}`)
		assertClientRepositoryError(t, response, http.StatusBadRequest, "pairing code is not active")
		clients, err := base.ListClients(t.Context())
		if err != nil || len(clients) != 1 || strings.Contains(response.Body.String(), `"token"`) || server.pairing.pending != nil {
			t.Fatalf("clients=%#v err=%v body=%s pending=%#v", clients, err, response.Body.String(), server.pairing.pending)
		}
	})
}

func TestPairingTimerGenerationAndLifecycleCleanup(t *testing.T) {
	t.Run("stale generation cannot clear replacement", func(t *testing.T) {
		server := newClientRepositoryTestServer(t, store.NewMemoryStore())
		if err := server.pairing.gate.Acquire(t.Context(), 1); err != nil {
			t.Fatal(err)
		}
		first := &pairingPending{kind: pairingPendingStart, expiresAt: time.Now().UTC().Add(time.Hour), plaintextCode: "first"}
		server.installPairingPendingLocked(first)
		server.clearPairingPendingLocked()
		second := &pairingPending{kind: pairingPendingStart, expiresAt: time.Now().UTC().Add(time.Hour), plaintextCode: "second"}
		server.installPairingPendingLocked(second)
		server.pairing.gate.Release(1)

		server.clearPairingGeneration(first.generation)
		if server.pairing.pending != second || second.plaintextCode != "second" || first.plaintextCode != "" {
			t.Fatalf("stale generation changed replacement: first=%#v second=%#v pending=%#v", first, second, server.pairing.pending)
		}
	})

	t.Run("expiry timer", func(t *testing.T) {
		server := newClientRepositoryTestServer(t, store.NewMemoryStore())
		if err := server.pairing.gate.Acquire(t.Context(), 1); err != nil {
			t.Fatal(err)
		}
		pending := &pairingPending{kind: pairingPendingStart, expiresAt: time.Now().UTC().Add(20 * time.Millisecond), plaintextCode: "expiring"}
		server.installPairingPendingLocked(pending)
		server.pairing.gate.Release(1)

		select {
		case <-pending.done:
		case <-time.After(time.Second):
			t.Fatal("expiry timer did not clear pending secret")
		}
		if server.pairing.pending != nil || pending.plaintextCode != "" {
			t.Fatalf("timer cleanup pending=%#v plaintext=%q", server.pairing.pending, pending.plaintextCode)
		}
	})

	t.Run("lifecycle cancellation", func(t *testing.T) {
		lifecycle, cancel := context.WithCancel(context.Background())
		server := &Server{store: store.NewMemoryStore(), pairing: newPairingCoordinator(), lifecycleCtx: lifecycle}
		if err := server.pairing.gate.Acquire(t.Context(), 1); err != nil {
			t.Fatal(err)
		}
		pending := &pairingPending{kind: pairingPendingClaim, expiresAt: time.Now().UTC().Add(time.Hour), plaintextToken: "token"}
		server.installPairingPendingLocked(pending)
		server.pairing.gate.Release(1)
		cancel()

		select {
		case <-pending.done:
		case <-time.After(time.Second):
			t.Fatal("lifecycle cancellation did not clear pending secret")
		}
		if server.pairing.pending != nil || pending.plaintextToken != "" {
			t.Fatalf("lifecycle cleanup pending=%#v plaintext=%q", server.pairing.pending, pending.plaintextToken)
		}
	})
}

func TestPairingSemaphoreDeadlineExceededIsGatewayTimeout(t *testing.T) {
	for _, testCase := range []struct {
		name string
		call func(*Server, *http.Request) *httptest.ResponseRecorder
	}{
		{name: "start", call: func(server *Server, request *http.Request) *httptest.ResponseRecorder {
			return invokePairingStart(server, request)
		}},
		{name: "claim", call: func(server *Server, request *http.Request) *httptest.ResponseRecorder {
			response := httptest.NewRecorder()
			server.claimPairing(response, request)
			return response
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := newClientRepositoryTestServer(t, store.NewMemoryStore())
			if err := server.pairing.gate.Acquire(t.Context(), 1); err != nil {
				t.Fatal(err)
			}
			defer server.pairing.gate.Release(1)
			ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			defer cancel()
			body := ""
			path := "/api/pairing/start"
			if testCase.name == "claim" {
				path = "/api/pairing/claim"
				body = `{"pairing_id":"pair","code":"code","client_name":"client"}`
			}
			request := clientRepositoryRequest(http.MethodPost, path, body).WithContext(ctx)

			response := testCase.call(server, request)
			assertClientRepositoryError(t, response, http.StatusGatewayTimeout, "pairing request timed out")
		})
	}
}

func TestClientListAndRevokeSafeStoreFailureProjection(t *testing.T) {
	private := []string{"secret", "private-path", "unknown_outcome", "corrupt"}
	for _, testCase := range []struct {
		name       string
		code       store.StoreErrorCode
		wantStatus int
		wantError  string
	}{
		{name: "list timeout", code: store.StoreErrorTimeout, wantStatus: http.StatusGatewayTimeout, wantError: "client list request timed out"},
		{name: "list unavailable", code: store.StoreErrorUnavailable, wantStatus: http.StatusServiceUnavailable, wantError: "clients are temporarily unavailable"},
		{name: "list corrupt", code: store.StoreErrorCorrupt, wantStatus: http.StatusServiceUnavailable, wantError: "clients are temporarily unavailable"},
		{name: "list unknown", code: store.StoreErrorUnknownOutcome, wantStatus: http.StatusServiceUnavailable, wantError: "clients are temporarily unavailable"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			faults := &clientRepositoryFaultStore{Repository: store.NewMemoryStore()}
			faults.listClientsFn = func(context.Context) ([]app.Client, error) {
				return nil, clientRepositoryStoreError(testCase.code, store.OperationClientList)
			}
			server := newClientRepositoryTestServer(t, faults)
			response := httptest.NewRecorder()
			server.listClients(response, clientRepositoryRequest(http.MethodGet, "/api/clients", ""))
			assertClientRepositoryError(t, response, testCase.wantStatus, testCase.wantError, private...)
		})
	}

	for _, testCase := range []struct {
		name       string
		code       store.StoreErrorCode
		wantStatus int
		wantError  string
	}{
		{name: "revoke not found", code: store.StoreErrorNotFound, wantStatus: http.StatusNotFound, wantError: "client not found"},
		{name: "revoke timeout", code: store.StoreErrorTimeout, wantStatus: http.StatusGatewayTimeout, wantError: "client revoke request timed out"},
		{name: "revoke unavailable", code: store.StoreErrorUnavailable, wantStatus: http.StatusServiceUnavailable, wantError: "clients are temporarily unavailable"},
		{name: "revoke corrupt", code: store.StoreErrorCorrupt, wantStatus: http.StatusServiceUnavailable, wantError: "clients are temporarily unavailable"},
		{name: "revoke unknown", code: store.StoreErrorUnknownOutcome, wantStatus: http.StatusServiceUnavailable, wantError: "clients are temporarily unavailable"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			faults := &clientRepositoryFaultStore{Repository: store.NewMemoryStore()}
			faults.revokeClientFn = func(context.Context, string) (app.Client, error) {
				return app.Client{}, clientRepositoryStoreError(testCase.code, store.OperationClientRevoke)
			}
			server := newClientRepositoryTestServer(t, faults)
			request := clientRepositoryRequest(http.MethodPost, "/api/clients/client-a/revoke", `{}`)
			request.SetPathValue("id", "client-a")
			response := httptest.NewRecorder()
			server.revokeClient(response, request)
			assertClientRepositoryError(t, response, testCase.wantStatus, testCase.wantError, private...)
		})
	}
}

func TestAuthenticationSafeStoreFailureProjection(t *testing.T) {
	private := []string{"secret", "private-path", "unknown_outcome", "corrupt"}
	for _, stage := range []string{"lookup", "touch"} {
		for _, testCase := range []struct {
			name       string
			code       store.StoreErrorCode
			wantStatus int
			wantError  string
		}{
			{name: "timeout", code: store.StoreErrorTimeout, wantStatus: http.StatusGatewayTimeout, wantError: "authentication request timed out"},
			{name: "unavailable", code: store.StoreErrorUnavailable, wantStatus: http.StatusServiceUnavailable, wantError: "authentication is temporarily unavailable"},
			{name: "corrupt", code: store.StoreErrorCorrupt, wantStatus: http.StatusServiceUnavailable, wantError: "authentication is temporarily unavailable"},
			{name: "unknown", code: store.StoreErrorUnknownOutcome, wantStatus: http.StatusServiceUnavailable, wantError: "authentication is temporarily unavailable"},
		} {
			t.Run(stage+" "+testCase.name, func(t *testing.T) {
				faults := &clientRepositoryFaultStore{Repository: store.NewMemoryStore()}
				client := app.Client{ID: "client-auth", OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID, Name: "auth", CreatedAt: time.Now().UTC()}
				if stage == "lookup" {
					faults.findClientFn = func(context.Context, string) (app.Client, bool, error) {
						return app.Client{}, false, clientRepositoryStoreError(testCase.code, store.OperationClientFindTokenHash)
					}
				} else {
					faults.findClientFn = func(context.Context, string) (app.Client, bool, error) { return client, true, nil }
					faults.touchClientFn = func(context.Context, string) (app.Client, bool, error) {
						return app.Client{}, false, clientRepositoryStoreError(testCase.code, store.OperationClientTouch)
					}
				}
				server := newClientRepositoryTestServer(t, faults)
				nextCalled := false
				handler := server.withAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { nextCalled = true }))
				request := clientRepositoryRequest(http.MethodGet, "/api/clients", "")
				request.Header.Set("Authorization", "Bearer caller-token")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				assertClientRepositoryError(t, response, testCase.wantStatus, testCase.wantError, private...)
				if nextCalled {
					t.Fatal("authentication Store failure reached protected handler")
				}
			})
		}
	}

	for _, testCase := range []struct {
		name  string
		find  bool
		touch bool
	}{
		{name: "invalid token"},
		{name: "concurrently revoked", find: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			faults := &clientRepositoryFaultStore{Repository: store.NewMemoryStore()}
			client := app.Client{ID: "client-auth", OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID, Name: "auth", CreatedAt: time.Now().UTC()}
			faults.findClientFn = func(context.Context, string) (app.Client, bool, error) { return client, testCase.find, nil }
			faults.touchClientFn = func(context.Context, string) (app.Client, bool, error) { return client, testCase.touch, nil }
			server := newClientRepositoryTestServer(t, faults)
			handler := server.withAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("invalid credential reached protected handler") }))
			request := clientRepositoryRequest(http.MethodGet, "/api/clients", "")
			request.Header.Set("Authorization", "Bearer caller-token")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			assertClientRepositoryError(t, response, http.StatusUnauthorized, "valid bearer token required")
		})
	}
}
