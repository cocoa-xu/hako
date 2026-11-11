package server

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const sessionCookie = "hako_session"

func (s *Server) loadAdminKey() error {
	path := filepath.Join(s.directory, "admin.key")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		_, err = file.WriteString(randomSecret(32) + "\n")
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	} else if !os.IsExist(err) {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("admin.key must be a private regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	s.adminKey = strings.TrimSpace(string(data))
	if len(s.adminKey) != 64 || strings.Trim(s.adminKey, "0123456789abcdef") != "" {
		return fmt.Errorf("invalid admin key")
	}
	if s.registry.AdminKeyHash != hashSecret(s.adminKey) {
		s.registry.AdminKeyHash = hashSecret(s.adminKey)
		s.registry.Sessions = map[string]time.Time{}
		return s.saveRegistry(s.registry)
	}
	return nil
}

func (s *Server) authorized(r *http.Request) bool {
	if s.adminAuth == "external" {
		return true
	}
	if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return subtle.ConstantTimeCompare([]byte(hashSecret(token)), []byte(hashSecret(s.adminKey))) == 1
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	expires, exists := s.registry.Sessions[hashSecret(cookie.Value)]
	return exists && time.Now().Before(expires)
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			http.Error(w, "Sign in to continue.", http.StatusUnauthorized)
			return
		}
		cli := s.adminAuth == "builtin" && r.Header.Get("Authorization") != "" || s.adminAuth == "external" && r.Header.Get("X-Hako-Request") == "cli"
		if r.Method != "GET" && r.Method != "HEAD" && !cli && r.Header.Get("Origin") == "" {
			http.Error(w, "A same-origin request is required.", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.adminAuth == "external" {
		http.Error(w, "Administrator authentication is managed externally.", http.StatusNotFound)
		return
	}
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || !s.authorized(r) {
		http.Error(w, "Invalid administrator key.", http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copyRegistry()
	now := time.Now()
	for hash, expires := range next.Sessions {
		if !now.Before(expires) {
			delete(next.Sessions, hash)
		}
	}
	if len(next.Sessions) >= 128 {
		http.Error(w, "Too many active sessions.", http.StatusTooManyRequests)
		return
	}
	expires := now.Add(12 * time.Hour)
	value := randomSecret(32)
	next.Sessions[hashSecret(value)] = expires
	if err := s.saveRegistry(next); err != nil {
		http.Error(w, "Could not create session.", 500)
		return
	}
	s.registry = next
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: value, Path: "/", HttpOnly: true, Secure: s.secureRequest(r), SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: 12 * 3600})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if s.adminAuth == "external" {
		http.Error(w, "Administrator authentication is managed externally.", http.StatusNotFound)
		return
	}
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		s.mu.Lock()
		next := s.copyRegistry()
		delete(next.Sessions, hashSecret(cookie.Value))
		err := s.saveRegistry(next)
		if err == nil {
			s.registry = next
		}
		s.mu.Unlock()
		if err != nil {
			http.Error(w, "Could not close session.", 500)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secureRequest(r), SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) secureRequest(r *http.Request) bool {
	return r.TLS != nil || (s.publicURL != nil && r.Host == s.publicURL.Host && s.publicURL.Scheme == "https")
}
