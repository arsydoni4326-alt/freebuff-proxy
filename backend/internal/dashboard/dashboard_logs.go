package dashboard

import (
	"net/http"
	"strings"
)

// --- logs ---

type logsData struct {
	Enabled   bool       `json:"enabled"`
	Level     string     `json:"level"`
	Msg       string     `json:"msg"`
	HasFilter bool       `json:"has_filter"`
	Entries   []logEntry `json:"entries"`
}

type logEntry struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
	Fields  string `json:"fields"`
}

func (d *Dashboard) logsData(r *http.Request) logsData {
	ld := logsData{Enabled: d.logs != nil}
	if d.logs == nil {
		return ld
	}
	level := ""
	msg := ""
	if r != nil && r.URL != nil {
		level = strings.TrimSpace(r.URL.Query().Get("level"))
		msg = strings.TrimSpace(r.URL.Query().Get("msg"))
	}
	ld.Level = strings.ToLower(level)
	ld.Msg = msg
	ld.HasFilter = level != "" || msg != ""
	msgLower := strings.ToLower(msg)
	for _, e := range d.logs.Recent(200) {
		if level != "" && !strings.EqualFold(e.Level, level) {
			continue
		}
		if msg != "" && !strings.Contains(strings.ToLower(e.Message), msgLower) {
			continue
		}
		ld.Entries = append(ld.Entries, logEntry{
			Time:    e.Time,
			Level:   e.Level,
			Message: e.Message,
			Fields:  strings.Join(e.Fields, "  "),
		})
	}
	return ld
}
