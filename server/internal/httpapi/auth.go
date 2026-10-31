package httpapi

import (
	"cmp"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"sigismundo/internal/config"
	"sigismundo/internal/session"
)

const tokenTTL = 12 * time.Hour

type claims struct {
	session.User
	Exp int64 `json:"exp"`
}

// signToken issues the bearer token the Activity uses on /api/ws.
func signToken(u session.User) string {
	b, _ := json.Marshal(claims{User: u, Exp: time.Now().Add(tokenTTL).Unix()})
	body := base64.RawURLEncoding.EncodeToString(b)
	return body + "." + sign(config.SessionSecret, body)
}

// relayToken lets a viewer read the stream from the relay (relay/src), which
// verifies it with the RELAY_SECRET it shares with this server.
func relayToken() string {
	b, _ := json.Marshal(map[string]int64{"exp": time.Now().Add(tokenTTL).Unix()})
	body := base64.RawURLEncoding.EncodeToString(b)
	return body + "." + sign(config.RelaySecret, body)
}

func verifyToken(tok string) (session.User, error) {
	body, sig, ok := strings.Cut(tok, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(sign(config.SessionSecret, body))) {
		return session.User{}, errors.New("bad token")
	}
	b, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return session.User{}, err
	}
	var c claims
	if err := json.Unmarshal(b, &c); err != nil {
		return session.User{}, err
	}
	if time.Now().Unix() > c.Exp {
		return session.User{}, errors.New("token expired")
	}
	return c.User, nil
}

func sign(key []byte, body string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

var discordHTTP = &http.Client{Timeout: 15 * time.Second}

// exchange trades the Activity's authorize code for a user access token.
func (s *Server) exchange(code string) (string, error) {
	resp, err := discordHTTP.PostForm("https://discord.com/api/v10/oauth2/token", url.Values{
		"client_id":     {s.appID},
		"client_secret": {config.ClientSecret},
		"grant_type":    {"authorization_code"},
		"code":          {code},
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("token exchange: %s %s", resp.Status, out.Error)
	}
	return out.AccessToken, nil
}

func fetchUser(accessToken string) (session.User, error) {
	req, _ := http.NewRequest(http.MethodGet, "https://discord.com/api/v10/users/@me", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := discordHTTP.Do(req)
	if err != nil {
		return session.User{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return session.User{}, fmt.Errorf("users/@me: %s", resp.Status)
	}
	var u struct {
		ID         string `json:"id"`
		Username   string `json:"username"`
		GlobalName string `json:"global_name"`
		Avatar     string `json:"avatar"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return session.User{}, err
	}
	return session.User{ID: u.ID, Name: cmp.Or(u.GlobalName, u.Username), Avatar: avatarURL(u.ID, u.Avatar)}, nil
}

func avatarURL(id, hash string) string {
	if hash == "" {
		return ""
	}
	return fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.png?size=64", id, hash)
}

// token handles POST /api/token {code}: the Activity's OAuth2 code exchange.
// The access token goes back for discordSdk.commands.authenticate, the signed
// token identifies the user to this server.
func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil || in.Code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	access, err := s.exchange(in.Code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	u, err := fetchUser(access)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"access_token": access, "token": signToken(u)})
}

// devToken skips Discord entirely for local testing in a plain browser
// (GO_ENV=development and DEV_AUTH=true only).
func (s *Server) devToken(w http.ResponseWriter, r *http.Request) {
	id := cmp.Or(r.URL.Query().Get("id"), "1")
	u := session.User{ID: id, Name: cmp.Or(r.URL.Query().Get("name"), "dev-"+id)}
	writeJSON(w, map[string]any{"token": signToken(u)})
}
