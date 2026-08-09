// Package index handles text chunking, embedding, storage and retrieval.
// MVP uses simple sliding-window chunking + brute-force cosine similarity.
package index

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	chunkSize       = 500
	chunkOverlap    = 100
	dbBusyTimeoutMs = 5000
)

// EmbedFunc is a function that takes text and returns a float32 vector.
type EmbedFunc func(text string) ([]float32, error)

// Chunk represents a segment of transcript with its vector embedding.
type Chunk struct {
	ID      string    `json:"id"`
	TaskID  string    `json:"task_id"`
	Content string    `json:"content"`
	Vec     []float32 `json:"-"`
	Pos     int       `json:"position"`
}

// SearchResult is a ranked retrieval hit.
type SearchResult struct {
	Chunk
	Score float64 `json:"score"`
}

// Store manages the SQLite-backed index.
type Store struct {
	db *sql.DB
	mu sync.Mutex
}

// OpenStore opens (or creates) the SQLite index database.
func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout="+fmt.Sprint(dbBusyTimeoutMs))
	if err != nil {
		return nil, fmt.Errorf("index: open db: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite 单写

	if err := migrate(db); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// IndexTask chunks the text, embeds each chunk, and stores them.
func (s *Store) IndexTask(embedFn EmbedFunc, taskID, text string) (int, error) {
	chunks := split(text, chunkSize, chunkOverlap)
	if len(chunks) == 0 {
		return 0, nil
	}

	for i, c := range chunks {
		vec, err := embedFn(c)
		if err != nil {
			return 0, fmt.Errorf("index: embed chunk %d: %w", i, err)
		}

		id := fmt.Sprintf("%s_%d", taskID, i)
		vecJSON, _ := json.Marshal(vec)
		if err := s.insertChunk(id, taskID, c, vecJSON, i); err != nil {
			return 0, fmt.Errorf("index: insert chunk %d: %w", i, err)
		}
	}
	return len(chunks), nil
}

// Search retrieves the top-k chunks by cosine similarity.
func (s *Store) Search(embedFn EmbedFunc, query string, topK int) ([]SearchResult, error) {
	qVec, err := embedFn(query)
	if err != nil {
		return nil, fmt.Errorf("index: embed query: %w", err)
	}

	rows, err := s.db.Query("SELECT id, task_id, content, embedding FROM chunks ORDER BY position")
	if err != nil {
		return nil, fmt.Errorf("index: query all: %w", err)
	}
	defer rows.Close()

	var scored []SearchResult
	for rows.Next() {
		var id, taskID, content string
		var vecJSON []byte
		if err := rows.Scan(&id, &taskID, &content, &vecJSON); err != nil {
			continue
		}
		var vec []float32
		json.Unmarshal(vecJSON, &vec)
		score := cosineSimilarity(qVec, vec)
		if score > 0.3 {
			scored = append(scored, SearchResult{
				Chunk: Chunk{ID: id, TaskID: taskID, Content: content, Vec: vec},
				Score: score,
			})
		}
	}

	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })

	if len(scored) > topK {
		scored = scored[:topK]
	}
	return scored, nil
}

func (s *Store) insertChunk(id, taskID, content string, vecJSON []byte, pos int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(
		`INSERT INTO chunks (id, task_id, content, embedding, position, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, taskID, content, vecJSON, pos, now,
	)
	return err
}

// ── helpers ──

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS chunks (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			content TEXT NOT NULL,
			embedding BLOB NOT NULL,
			position INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS idx_chunks_task ON chunks(task_id);
	`)
	return err
}

func split(text string, size, overlap int) []string {
	runes := []rune(text)
	if len(runes) <= size {
		return []string{text}
	}

	var chunks []string
	for start := 0; start < len(runes); start += size - overlap {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		chunk := strings.TrimSpace(string(runes[start:end]))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		if end == len(runes) {
			break
		}
	}
	return chunks
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(na) * math.Sqrt(nb)
	if denom == 0 {
		return 0
	}
	return dot / denom
}
