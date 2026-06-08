package main

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore is the production Store backed by Postgres.
type PostgresStore struct {
	pool *pgxpool.Pool
}

const schema = `
CREATE TABLE IF NOT EXISTS posts (
	id         BIGSERIAL PRIMARY KEY,
	author     TEXT NOT NULL,
	content    TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	edited_at  TIMESTAMPTZ
);
CREATE TABLE IF NOT EXISTS likes (
	post_id BIGINT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
	nick    TEXT NOT NULL,
	PRIMARY KEY (post_id, nick)
);
CREATE TABLE IF NOT EXISTS comments (
	id         BIGSERIAL PRIMARY KEY,
	post_id    BIGINT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
	author     TEXT NOT NULL,
	content    TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS users (
	id         BIGSERIAL PRIMARY KEY,
	name       TEXT NOT NULL,
	login      TEXT NOT NULL UNIQUE,
	pass_hash  TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS sessions (
	token      TEXT PRIMARY KEY,
	user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
`

func NewPostgresStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	// Retry ping a few times — Postgres may still be starting in compose.
	var pingErr error
	for i := 0; i < 10; i++ {
		if pingErr = pool.Ping(ctx); pingErr == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if pingErr != nil {
		return nil, pingErr
	}
	if _, err := pool.Exec(ctx, schema); err != nil {
		return nil, err
	}
	return &PostgresStore{pool: pool}, nil
}

func (s *PostgresStore) ListPosts(ctx context.Context, viewer string) ([]Post, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.id, p.author, p.content, p.created_at, p.edited_at,
		       (SELECT count(*) FROM likes l WHERE l.post_id = p.id),
		       (SELECT count(*) FROM comments c WHERE c.post_id = p.id),
		       EXISTS(SELECT 1 FROM likes l WHERE l.post_id = p.id AND l.nick = $1)
		FROM posts p
		ORDER BY p.id DESC`, viewer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Post
	for rows.Next() {
		var p Post
		if err := rows.Scan(&p.ID, &p.Author, &p.Content, &p.CreatedAt, &p.EditedAt, &p.Likes, &p.Comments, &p.Liked); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *PostgresStore) CreatePost(ctx context.Context, author, content string) (Post, error) {
	var p Post
	p.Author, p.Content = author, content
	err := s.pool.QueryRow(ctx,
		`INSERT INTO posts(author, content) VALUES($1,$2) RETURNING id, created_at`,
		author, content).Scan(&p.ID, &p.CreatedAt)
	return p, err
}

func (s *PostgresStore) DeletePost(ctx context.Context, id int64, author string) error {
	ct, err := s.pool.Exec(ctx, `DELETE FROM posts WHERE id=$1 AND author=$2`, id, author)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return errors.New("not found or not your post")
	}
	return nil
}

func (s *PostgresStore) UpdatePost(ctx context.Context, id int64, author, content string) (Post, error) {
	var p Post
	err := s.pool.QueryRow(ctx,
		`UPDATE posts SET content=$1, edited_at=now() WHERE id=$2 AND author=$3
		 RETURNING id, author, content, created_at, edited_at`,
		content, id, author).Scan(&p.ID, &p.Author, &p.Content, &p.CreatedAt, &p.EditedAt)
	if err == pgx.ErrNoRows {
		return Post{}, errors.New("not found or not your post")
	}
	return p, err
}

func (s *PostgresStore) ToggleLike(ctx context.Context, postID int64, who string) (bool, int, error) {
	ct, err := s.pool.Exec(ctx, `DELETE FROM likes WHERE post_id=$1 AND nick=$2`, postID, who)
	if err != nil {
		return false, 0, err
	}
	liked := false
	if ct.RowsAffected() == 0 {
		if _, err := s.pool.Exec(ctx, `INSERT INTO likes(post_id, nick) VALUES($1,$2) ON CONFLICT DO NOTHING`, postID, who); err != nil {
			return false, 0, err
		}
		liked = true
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM likes WHERE post_id=$1`, postID).Scan(&n); err != nil {
		return false, 0, err
	}
	return liked, n, nil
}

func (s *PostgresStore) ListComments(ctx context.Context, postID int64) ([]Comment, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, post_id, author, content, created_at FROM comments WHERE post_id=$1 ORDER BY id ASC`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.PostID, &c.Author, &c.Content, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *PostgresStore) AddComment(ctx context.Context, postID int64, author, content string) (Comment, error) {
	var c Comment
	c.PostID, c.Author, c.Content = postID, author, content
	err := s.pool.QueryRow(ctx,
		`INSERT INTO comments(post_id, author, content) VALUES($1,$2,$3) RETURNING id, created_at`,
		postID, author, content).Scan(&c.ID, &c.CreatedAt)
	return c, err
}

func (s *PostgresStore) CreateUser(ctx context.Context, name, login, passHash string) (*User, error) {
	var u User
	u.Name, u.Login, u.PassHash = name, login, passHash
	err := s.pool.QueryRow(ctx,
		`INSERT INTO users(name, login, pass_hash) VALUES($1,$2,$3) RETURNING id, created_at`,
		name, login, passHash).Scan(&u.ID, &u.CreatedAt)
	if err != nil {
		if pgErr, ok := err.(*pgconn.PgError); ok && pgErr.Code == "23505" {
			return nil, errLoginTaken
		}
		return nil, err
	}
	return &u, nil
}

func (s *PostgresStore) GetUserByLogin(ctx context.Context, login string) (*User, bool, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, login, pass_hash, created_at FROM users WHERE login=$1`, login).
		Scan(&u.ID, &u.Name, &u.Login, &u.PassHash, &u.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &u, true, nil
}

func (s *PostgresStore) CreateSession(ctx context.Context, token string, userID int64) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO sessions(token, user_id) VALUES($1,$2)`, token, userID)
	return err
}

func (s *PostgresStore) GetUserBySession(ctx context.Context, token string) (*User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`SELECT u.id, u.name, u.login, u.pass_hash, u.created_at
		 FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token=$1`, token).
		Scan(&u.ID, &u.Name, &u.Login, &u.PassHash, &u.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *PostgresStore) DeleteSession(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token=$1`, token)
	return err
}
