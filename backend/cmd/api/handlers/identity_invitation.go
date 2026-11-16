package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

const invitationCookie = "__Host-moneybag-id-invitation"

type invitationContextKey struct{}
type identityFlowContext struct {
	Invitation string
	Invalid    bool
}
type invitationTicket struct{ Token, State string }
type invitationResponseWriter struct {
	http.ResponseWriter
	token string
	key   [32]byte
}

func (w *invitationResponseWriter) WriteHeader(status int) {
	if status == http.StatusSeeOther {
		value := ""
		age := -1
		if w.token != "" {
			location, err := url.Parse(w.Header().Get("Location"))
			if err == nil && location.Query().Get("state") != "" {
				value = encodeInvitation(invitationTicket{Token: w.token, State: location.Query().Get("state")}, w.key)
				age = 600
			}
		}
		http.SetCookie(w.ResponseWriter, invitationHTTP(value, age))
	}
	w.ResponseWriter.WriteHeader(status)
}
func invitationHTTP(value string, age int) *http.Cookie {
	return &http.Cookie{Name: invitationCookie, Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: age}
}
func validInvitationToken(value string) bool {
	if len(value) > 512 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func invitationMAC(payload string, key [32]byte) []byte {
	scope := hmac.New(sha256.New, key[:])
	scope.Write([]byte("moneybag invitation cookie key v1"))
	mac := hmac.New(sha256.New, scope.Sum(nil))
	mac.Write([]byte("moneybag invitation cookie v1\x00"))
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}
func encodeInvitation(ticket invitationTicket, key [32]byte) string {
	payload, _ := json.Marshal(ticket)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + base64.RawURLEncoding.EncodeToString(invitationMAC(encoded, key))
}
func decodeInvitation(value string, key [32]byte) (invitationTicket, error) {
	var ticket invitationTicket
	if len(value) > 2048 {
		return ticket, errors.New("invalid invitation ticket")
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return ticket, errors.New("invalid invitation ticket")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, invitationMAC(parts[0], key)) {
		return ticket, errors.New("invalid invitation ticket")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ticket, err
	}
	err = json.Unmarshal(payload, &ticket)
	return ticket, err
}
