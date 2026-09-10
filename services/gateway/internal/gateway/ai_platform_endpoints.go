package gateway

import "net/http"

func (s *Server) requireAIPlatformLogin(w http.ResponseWriter, r *http.Request) bool {
	if !browserControlRequestAllowed(w, r) {
		return false
	}
	if s.aiPlatformLogin == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "AI platform login is unavailable", "code": "browser_controller_unavailable"})
		return false
	}
	return true
}
func (s *Server) getAIPlatformLogin(w http.ResponseWriter, r *http.Request) {
	if !s.requireAIPlatformLogin(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.aiPlatformLogin.Overview(r.Context()))
}
func (s *Server) openAIPlatformLogin(w http.ResponseWriter, r *http.Request) {
	s.aiPlatformLoginAction(w, r, true)
}
func (s *Server) checkAIPlatformLogin(w http.ResponseWriter, r *http.Request) {
	s.aiPlatformLoginAction(w, r, false)
}
func (s *Server) aiPlatformLoginAction(w http.ResponseWriter, r *http.Request, open bool) {
	if !s.requireAIPlatformLogin(w, r) {
		return
	}
	var input struct{}
	if err := readBrowserControlJSON(w, r, &input, false); err != nil {
		writeBrowserControlError(w, browsercontrolInvalidRequest())
		return
	}
	p := r.PathValue("provider")
	switch p {
	case "chatgpt", "claude", "gemini", "grok":
	default:
		writeBrowserControlError(w, browsercontrolInvalidRequest())
		return
	}
	if open {
		status, err := s.aiPlatformLogin.OpenLogin(r.Context(), p)
		if err != nil {
			writeBrowserControlError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, status)
		return
	}
	status, err := s.aiPlatformLogin.Check(r.Context(), p)
	if err != nil {
		writeBrowserControlError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}
