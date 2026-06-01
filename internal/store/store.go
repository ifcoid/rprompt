// Package store menyimpan session id Claude Code per chat Telegram, dengan
// persistensi sederhana ke file JSON agar bertahan setelah restart.
package store

import (
	"encoding/json"
	"os"
	"strconv"
	"sync"
)

// Store aman dipakai dari banyak goroutine.
type Store struct {
	mu       sync.Mutex
	path     string
	sessions map[int64]string
}

// New memuat store dari file (jika ada).
func New(path string) (*Store, error) {
	s := &Store{path: path, sessions: map[int64]string{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	// File disimpan dengan key string (JSON object), konversi ke int64.
	raw := map[string]string{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	for k, v := range raw {
		if id, err := strconv.ParseInt(k, 10, 64); err == nil {
			s.sessions[id] = v
		}
	}
	return s, nil
}

// Get mengembalikan session id tersimpan untuk chat (kosong bila belum ada).
func (s *Store) Get(chatID int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[chatID]
}

// Set menyimpan session id untuk chat dan mem-flush ke disk.
func (s *Store) Set(chatID int64, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sessionID == "" {
		return nil
	}
	s.sessions[chatID] = sessionID
	return s.flush()
}

// Reset menghapus sesi sebuah chat (memulai percakapan baru).
func (s *Store) Reset(chatID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, chatID)
	return s.flush()
}

// flush menulis state ke disk. Pemanggil harus memegang s.mu.
func (s *Store) flush() error {
	raw := make(map[string]string, len(s.sessions))
	for k, v := range s.sessions {
		raw[strconv.FormatInt(k, 10)] = v
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
