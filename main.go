package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

//go:embed web
var webFS embed.FS

// Post is a single tweet.
type Post struct {
	ID        int64     `json:"id"`
	Author    string    `json:"author"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	EditedAt  *time.Time `json:"edited_at,omitempty"`
	Likes     int       `json:"likes"`
	Comments  int       `json:"comments"`
	Liked     bool      `json:"liked"`
}

// Comment is a reply on a post.
type Comment struct {
	ID        int64     `json:"id"`
	PostID    int64     `json:"post_id"`
	Author    string    `json:"author"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// errLoginTaken is returned by CreateUser when the login already exists.
var errLoginTaken = errors.New("login taken")

// Store is the storage abstraction; backed by memory locally and Postgres in prod.
type Store interface {
	ListPosts(ctx context.Context, viewer string) ([]Post, error)
	CreatePost(ctx context.Context, author, content string) (Post, error)
	DeletePost(ctx context.Context, id int64, author string) error
	UpdatePost(ctx context.Context, id int64, author, content string) (Post, error)
	ToggleLike(ctx context.Context, postID int64, who string) (liked bool, likes int, err error)
	ListComments(ctx context.Context, postID int64) ([]Comment, error)
	AddComment(ctx context.Context, postID int64, author, content string) (Comment, error)

	// Auth.
	CreateUser(ctx context.Context, name, login, passHash string) (*User, error)
	GetUserByLogin(ctx context.Context, login string) (*User, bool, error)
	CreateSession(ctx context.Context, token string, userID int64) error
	GetUserBySession(ctx context.Context, token string) (*User, error)
	DeleteSession(ctx context.Context, token string) error
}

var store Store

func main() {
	ctx := context.Background()

	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		pg, err := NewPostgresStore(ctx, dsn)
		if err != nil {
			log.Fatalf("postgres init: %v", err)
		}
		store = pg
		log.Println("storage: postgres")
	} else {
		store = NewMemStore()
		log.Println("storage: in-memory (set DATABASE_URL for postgres)")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/posts", handlePosts)
	mux.HandleFunc("/api/posts/", handlePostByID)
	mux.HandleFunc("/api/register", handleRegister)
	mux.HandleFunc("/api/login", handleLogin)
	mux.HandleFunc("/api/logout", handleLogout)
	mux.HandleFunc("/api/me", handleMe)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	// Serve embedded static frontend.
	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("/", http.FileServer(http.FS(sub)))

	addr := ":" + envOr("PORT", "8080")
	log.Printf("listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, withCORS(mux)))
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// viewer returns the acting login for read ops (for the per-user "liked" flag),
// or "" for an anonymous visitor.
func viewer(r *http.Request) string {
	if u := currentUser(r.Context(), r); u != nil {
		return u.Login
	}
	return ""
}

func withCORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Nick")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// /api/posts  -> GET list, POST create
func handlePosts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	switch r.Method {
	case http.MethodGet:
		posts, err := store.ListPosts(ctx, viewer(r))
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, posts)
	case http.MethodPost:
		u, ok := mustUser(w, r)
		if !ok {
			return
		}
		var body struct{ Content string `json:"content"` }
		json.NewDecoder(r.Body).Decode(&body)
		body.Content = strings.TrimSpace(body.Content)
		if body.Content == "" || len(body.Content) > 280 {
			writeJSON(w, 400, map[string]string{"error": "content must be 1..280 chars"})
			return
		}
		p, err := store.CreatePost(ctx, u.Login, body.Content)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 201, p)
	default:
		w.WriteHeader(405)
	}
}

// /api/posts/{id}                -> PUT edit, DELETE remove
// /api/posts/{id}/like           -> POST toggle like
// /api/posts/{id}/comments       -> GET list, POST add
func handlePostByID(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rest := strings.TrimPrefix(r.URL.Path, "/api/posts/")
	parts := strings.Split(rest, "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad id"})
		return
	}

	// Sub-resources.
	if len(parts) >= 2 {
		switch parts[1] {
		case "like":
			if r.Method != http.MethodPost {
				w.WriteHeader(405)
				return
			}
			u, ok := mustUser(w, r)
			if !ok {
				return
			}
			liked, likes, err := store.ToggleLike(ctx, id, u.Login)
			if err != nil {
				writeJSON(w, 500, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, 200, map[string]any{"liked": liked, "likes": likes})
			return
		case "comments":
			switch r.Method {
			case http.MethodGet:
				cs, err := store.ListComments(ctx, id)
				if err != nil {
					writeJSON(w, 500, map[string]string{"error": err.Error()})
					return
				}
				writeJSON(w, 200, cs)
			case http.MethodPost:
				u, ok := mustUser(w, r)
				if !ok {
					return
				}
				var body struct{ Content string `json:"content"` }
				json.NewDecoder(r.Body).Decode(&body)
				body.Content = strings.TrimSpace(body.Content)
				if body.Content == "" || len(body.Content) > 280 {
					writeJSON(w, 400, map[string]string{"error": "content must be 1..280 chars"})
					return
				}
				c, err := store.AddComment(ctx, id, u.Login, body.Content)
				if err != nil {
					writeJSON(w, 500, map[string]string{"error": err.Error()})
					return
				}
				writeJSON(w, 201, c)
			default:
				w.WriteHeader(405)
			}
			return
		}
	}

	// Direct post operations.
	u, ok := mustUser(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodDelete:
		if err := store.DeletePost(ctx, id, u.Login); err != nil {
			writeJSON(w, 403, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]string{"status": "deleted"})
	case http.MethodPut:
		var body struct{ Content string `json:"content"` }
		json.NewDecoder(r.Body).Decode(&body)
		body.Content = strings.TrimSpace(body.Content)
		if body.Content == "" || len(body.Content) > 280 {
			writeJSON(w, 400, map[string]string{"error": "content must be 1..280 chars"})
			return
		}
		p, err := store.UpdatePost(ctx, id, u.Login, body.Content)
		if err != nil {
			writeJSON(w, 403, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, p)
	default:
		w.WriteHeader(405)
	}
}
