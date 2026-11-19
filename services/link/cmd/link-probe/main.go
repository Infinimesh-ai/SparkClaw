// Command link-probe is an operator tool that acts as a link device: it
// joins a console domain with an access credential, stores its state in a
// private file and exercises the link layer (sync, send, follow a run).
//
//	link-probe join -state FILE -credential TEXT [-class phone] [-name NAME]
//	link-probe chat -state FILE -text "..." [-lan=false] [-session ID]
//	link-probe sync -state FILE
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"

	"github.com/Chiiz0/SparkClaw/services/link/consoleapi"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
	"github.com/Chiiz0/SparkClaw/services/link/linkclient"
)

type state struct {
	Membership consoleapi.Membership   `json:"membership"`
	Identity   identity.DeviceIdentity `json:"identity"`
	Private    string                  `json:"private"`
	Class      string                  `json:"class"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: link-probe {join|chat|sync|delete} -state FILE ...")
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	path := fs.String("state", "", "private state file")
	credential := fs.String("credential", "", "access credential")
	class := fs.String("class", contract.ClassPhone, "device class")
	name := fs.String("name", "link-probe", "device name")
	text := fs.String("text", "你好", "message to send")
	session := fs.String("session", "", "existing conversation id")
	lan := fs.Bool("lan", true, "allow the LAN path")
	_ = fs.Parse(os.Args[2:])
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	switch cmd {
	case "join":
		cred, err := consoleapi.ParseCredential(*credential)
		check(err)
		m, dev, err := consoleapi.NewClient(cred.Console, cred.TLSPin).Join(ctx, cred, consoleapi.JoinOptions{Name: *name, Platform: "probe"})
		check(err)
		st := state{Membership: m, Identity: dev.Identity, Private: crypto.Base64URL(dev.Private.BytesForDevStore()), Class: *class}
		raw, _ := json.MarshalIndent(st, "", " ")
		check(os.WriteFile(*path, raw, 0o600))
		fmt.Printf("joined %s as %s; host %v; grant %v\n", m.DomainID, dev.Identity.DeviceID, m.Host != nil, m.Grant != nil)
	case "chat", "sync", "delete":
		var st state
		raw, err := os.ReadFile(*path)
		check(err)
		check(json.Unmarshal(raw, &st))
		kb, err := crypto.DecodeBase64URL(st.Private)
		check(err)
		priv, err := crypto.Ed25519PrivateKeyFromBytes(kb)
		check(err)
		dev := identity.Device{Identity: st.Identity, Private: priv}
		keeper := linkclient.NewGrantKeeper(st.Membership, dev)
		if keeper.Host() == nil {
			_, err := keeper.RefreshHost(ctx)
			check(err)
		}
		opts, err := linkclient.FromMembership(st.Membership, dev, keeper, nil, contract.Device{Class: st.Class, Name: *name, App: "link-probe", Conditions: contract.Conditions{Screen: contract.ScreenSmall, Attachments: st.Class != contract.ClassWatch}})
		check(err)
		opts.DisableLAN = !*lan
		events := make(chan contract.Frame, 256)
		opts.OnEvent = func(f contract.Frame) {
			select {
			case events <- f:
			default:
			}
		}
		started := time.Now()
		c, err := linkclient.New(opts)
		check(err)
		c.Start(ctx)
		defer c.Close()
		wctx, wcancel := context.WithTimeout(ctx, 90*time.Second)
		check(c.WaitReady(wctx))
		wcancel()
		fmt.Printf("connected via %s in %s; features %d\n", c.Path(), time.Since(started).Round(time.Millisecond), len(c.Hello().Features))
		var changes contract.SyncChangesResult
		check(c.Request(ctx, contract.OpSyncChanges, contract.SyncChangesParams{}, &changes))
		fmt.Printf("sync: %d conversations, cursor %d\n", len(changes.Sessions), changes.Cursor)
		if cmd == "delete" {
			// -session takes a comma-separated list of conversation ids.
			for _, id := range strings.Split(*session, ",") {
				if id = strings.TrimSpace(id); id != "" {
					check(c.Request(ctx, contract.OpSessionDelete, contract.SessionRef{SessionID: id}, nil))
					fmt.Println("deleted", id)
				}
			}
			return
		}
		if cmd == "sync" {
			for _, h := range changes.Sessions {
				fmt.Printf("  %s %q seq=%d deleted=%v\n", h.ID, h.Title, h.LastSeq, h.Deleted)
			}
			return
		}
		sid := *session
		if sid == "" {
			var h contract.SessionHeader
			check(c.Request(ctx, contract.OpSessionCreate, contract.SessionCreateParams{Title: "链接层验证", ClientRequestID: fmt.Sprint(time.Now().UnixNano())}, &h))
			sid = h.ID
		}
		var sub contract.RunSubscribeResult
		check(c.Request(ctx, contract.OpRunSubscribe, contract.RunSubscribeParams{SessionID: sid}, &sub))
		var sent contract.MessageSendResult
		sendAt := time.Now()
		check(c.Request(ctx, contract.OpMessageSend, contract.MessageSendParams{SessionID: sid, ClientMessageID: fmt.Sprint(time.Now().UnixNano()), Content: *text, ClientTimezone: "Asia/Shanghai"}, &sent))
		fmt.Printf("sent to %s run %s\n", sid, sent.RunID)
		var first time.Duration
		for {
			select {
			case f := <-events:
				if f.Topic != contract.TopicRun {
					continue
				}
				var ev contract.RunEvent
				_ = json.Unmarshal(f.Data, &ev)
				if ev.Kind == "delta" && first == 0 {
					first = time.Since(sendAt)
				}
				if ev.Kind == "final" && ev.RunID == sent.RunID && ev.Status != nil {
					reply := ""
					if ev.Status.FinalMessage != nil {
						reply = ev.Status.FinalMessage.Content
					}
					fmt.Printf("final %s after %s (first delta %s)\nreply: %s\n", ev.Status.State, time.Since(sendAt).Round(time.Millisecond), first.Round(time.Millisecond), reply)
					return
				}
			case <-ctx.Done():
				check(ctx.Err())
			}
		}
	}
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "link-probe:", err)
		os.Exit(1)
	}
}
