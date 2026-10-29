package gui

import (
	"net/http"
	"strconv"
	"time"

	"github.com/hikariming/dsdock/internal/balance"
)

// usageReport is the token usage the overview charts, from local session logs.
func (s *Server) usageReport(w http.ResponseWriter, r *http.Request) (any, error) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 366 {
		days = 182
	}
	return s.usage.Scan(days, time.Now())
}

// balance asks DeepSeek with the key DSH has; see package balance.
func (s *Server) balance(w http.ResponseWriter, r *http.Request) (any, error) {
	return balance.Lookup(r.Context(), s.core.Home, nil), nil
}
