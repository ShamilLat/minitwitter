package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// User is a registered account. PassHash is never serialized.
type User struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Login     string    `json:"login"`
	PassHash  string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

const sessionCookie = "sid"

func newToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// currentUser resolves the logged-in user from the session cookie, or nil.
func currentUser(ctx context.Context, r *http.Request) *User {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	u, _ := store.GetUserBySession(ctx, c.Value)
	return u
}

// mustUser writes 401 and returns false if nobody is logged in.
func mustUser(w http.ResponseWriter, r *http.Request) (*User, bool) {
	u := currentUser(r.Context(), r)
	if u == nil {
		writeJSON(w, 401, map[string]string{"error": "нужно войти в аккаунт"})
		return nil, false
	}
	return u, true
}

func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return r.Header.Get("X-Forwarded-Proto") == "https"
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   30 * 24 * 3600,
	})
}

func normLogin(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// POST /api/register {name, login, password}
func handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var b struct{ Name, Login, Password string }
	json.NewDecoder(r.Body).Decode(&b)
	name := strings.TrimSpace(b.Name)
	login := normLogin(b.Login)
	if name == "" || len(name) > 50 {
		writeJSON(w, 400, map[string]string{"error": "имя: 1..50 символов"})
		return
	}
	if len(login) < 3 || len(login) > 30 || strings.ContainsAny(login, " @#") {
		writeJSON(w, 400, map[string]string{"error": "логин: 3..30 символов, без пробелов и @ #"})
		return
	}
	if len(b.Password) < 4 || len(b.Password) > 72 {
		writeJSON(w, 400, map[string]string{"error": "пароль: 4..72 символа"})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(b.Password), bcrypt.DefaultCost)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	u, err := store.CreateUser(r.Context(), name, login, string(hash))
	if err == errLoginTaken {
		writeJSON(w, 409, map[string]string{"error": "логин уже занят", "code": "login_taken"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	startSession(w, r, u)
}

// POST /api/login {login, password}
func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var b struct{ Login, Password string }
	json.NewDecoder(r.Body).Decode(&b)
	u, ok, err := store.GetUserByLogin(r.Context(), normLogin(b.Login))
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if !ok || bcrypt.CompareHashAndPassword([]byte(u.PassHash), []byte(b.Password)) != nil {
		writeJSON(w, 401, map[string]string{"error": "неверный логин или пароль"})
		return
	}
	startSession(w, r, u)
}

func startSession(w http.ResponseWriter, r *http.Request, u *User) {
	token := newToken()
	if err := store.CreateSession(r.Context(), token, u.ID); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	setSessionCookie(w, r, token)
	writeJSON(w, 200, u)
}

// POST /api/logout
func handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		store.DeleteSession(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		Secure:   requestIsHTTPS(r),
		MaxAge:   -1,
	})
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// GET /api/me -> user or null
func handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, currentUser(r.Context(), r))
}
