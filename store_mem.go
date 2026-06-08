package main

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// MemStore is an in-memory Store for local development.
type MemStore struct {
	mu       sync.Mutex
	seq      int64
	cseq     int64
	useq     int64
	posts    map[int64]*Post
	likes    map[int64]map[string]bool // postID -> set of nicks
	comments map[int64][]Comment
	users    map[string]*User // login -> user
	sessions map[string]int64 // token -> userID
}

func NewMemStore() *MemStore {
	m := &MemStore{
		posts:    map[int64]*Post{},
		likes:    map[int64]map[string]bool{},
		comments: map[int64][]Comment{},
		users:    map[string]*User{},
		sessions: map[string]int64{},
	}
	// Seed a couple of posts so the feed isn't empty.
	m.CreatePost(context.Background(), "jack", "just setting up my minitwttr")
	m.CreatePost(context.Background(), "anon", "Привет! Это лента mini-twitter 🐦")
	return m
}

func (m *MemStore) ListPosts(_ context.Context, viewer string) ([]Post, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Post, 0, len(m.posts))
	for _, p := range m.posts {
		cp := *p
		cp.Likes = len(m.likes[p.ID])
		cp.Comments = len(m.comments[p.ID])
		cp.Liked = m.likes[p.ID][viewer]
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

func (m *MemStore) CreatePost(_ context.Context, author, content string) (Post, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	p := &Post{ID: m.seq, Author: author, Content: content, CreatedAt: time.Now().UTC()}
	m.posts[p.ID] = p
	return *p, nil
}

func (m *MemStore) DeletePost(_ context.Context, id int64, author string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.posts[id]
	if !ok {
		return errors.New("not found")
	}
	if p.Author != author {
		return errors.New("not your post")
	}
	delete(m.posts, id)
	delete(m.likes, id)
	delete(m.comments, id)
	return nil
}

func (m *MemStore) UpdatePost(_ context.Context, id int64, author, content string) (Post, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.posts[id]
	if !ok {
		return Post{}, errors.New("not found")
	}
	if p.Author != author {
		return Post{}, errors.New("not your post")
	}
	p.Content = content
	now := time.Now().UTC()
	p.EditedAt = &now
	cp := *p
	cp.Likes = len(m.likes[id])
	cp.Comments = len(m.comments[id])
	return cp, nil
}

func (m *MemStore) ToggleLike(_ context.Context, postID int64, who string) (bool, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.posts[postID]; !ok {
		return false, 0, errors.New("not found")
	}
	set := m.likes[postID]
	if set == nil {
		set = map[string]bool{}
		m.likes[postID] = set
	}
	liked := false
	if set[who] {
		delete(set, who)
	} else {
		set[who] = true
		liked = true
	}
	return liked, len(set), nil
}

func (m *MemStore) ListComments(_ context.Context, postID int64) ([]Comment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cs := append([]Comment(nil), m.comments[postID]...)
	return cs, nil
}

func (m *MemStore) AddComment(_ context.Context, postID int64, author, content string) (Comment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.posts[postID]; !ok {
		return Comment{}, errors.New("not found")
	}
	m.cseq++
	c := Comment{ID: m.cseq, PostID: postID, Author: author, Content: content, CreatedAt: time.Now().UTC()}
	m.comments[postID] = append(m.comments[postID], c)
	return c, nil
}

func (m *MemStore) CreateUser(_ context.Context, name, login, passHash string) (*User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.users[login]; exists {
		return nil, errLoginTaken
	}
	m.useq++
	u := &User{ID: m.useq, Name: name, Login: login, PassHash: passHash, CreatedAt: time.Now().UTC()}
	m.users[login] = u
	return u, nil
}

func (m *MemStore) GetUserByLogin(_ context.Context, login string) (*User, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[login]
	if !ok {
		return nil, false, nil
	}
	cp := *u
	return &cp, true, nil
}

func (m *MemStore) CreateSession(_ context.Context, token string, userID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[token] = userID
	return nil
}

func (m *MemStore) GetUserBySession(_ context.Context, token string) (*User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	uid, ok := m.sessions[token]
	if !ok {
		return nil, nil
	}
	for _, u := range m.users {
		if u.ID == uid {
			cp := *u
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *MemStore) DeleteSession(_ context.Context, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, token)
	return nil
}
