package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// spec beschreibt ein Ereignis, das man in der Einstellung events einschalten kann.
type spec struct {
	key    string // Name in der Einstellung
	typ    string // Ereignistyp auf dem Bus
	device bool   // der Name des Druckers wird beim Server erfragt
	raw    bool   // Rohinhalt ins Log schreiben, solange die Felder nicht gemessen sind
	job    bool   // Job-Ereignis: je Job und Ergebnis nur einmal melden
	build  func(data json.RawMessage, dev string, cfg *settings, a *app) (note, bool)
}

var specs = []spec{
	{key: "job_started", typ: "com.prusa.connect.device.job.started", device: true, job: true,
		build: func(d json.RawMessage, dev string, _ *settings, _ *app) (note, bool) {
			return build("com.prusa.connect.device.job.started", d, dev)
		}},
	{key: "job_settled", typ: "com.prusa.connect.device.job.settled", device: true, job: true,
		build: func(d json.RawMessage, dev string, cfg *settings, _ *app) (note, bool) {
			var j jobData
			if json.Unmarshal(d, &j) != nil {
				return note{}, false
			}
			o := j.Outcome
			if o != "finished" && o != "failed" && o != "cancelled" {
				o = "unknown"
			}
			if !contains(cfg.Outcomes, o) {
				return note{}, false
			}
			return build("com.prusa.connect.device.job.settled", d, dev)
		}},
	{key: "printer_online", typ: "com.prusa.connect.device.arrived", device: true, raw: true,
		build: func(_ json.RawMessage, dev string, _ *settings, _ *app) (note, bool) {
			return note{title: "🟢 " + dev + ": verbunden", kind: "info"}, true
		}},
	{key: "printer_offline", typ: "com.prusa.connect.device.departed", device: true, raw: true,
		build: func(d json.RawMessage, dev string, _ *settings, _ *app) (note, bool) {
			var x struct {
				Reason string `json:"reason"`
			}
			_ = json.Unmarshal(d, &x)
			return note{title: "🔴 " + dev + ": getrennt", body: x.Reason, kind: "warning"}, true
		}},
	{key: "printer_issues", typ: "com.prusa.connect.device.issues.changed", device: true, raw: true,
		build: buildIssues},
	{key: "new_printer", typ: "com.prusa.connect.device.registration.announced",
		build: func(d json.RawMessage, _ string, _ *settings, _ *app) (note, bool) {
			var r struct {
				Code, Model, Serial, Firmware string
			}
			var m map[string]any
			if json.Unmarshal(d, &m) != nil {
				return note{}, false
			}
			str := func(k string) string { s, _ := m[k].(string); return s }
			r.Code, r.Model, r.Serial, r.Firmware = str("code"), str("model"), str("serial"), str("firmware")
			var lines []string
			for _, p := range [][2]string{{"Code", r.Code}, {"Modell", r.Model}, {"Seriennummer", r.Serial}, {"Firmware", r.Firmware}} {
				if p[1] != "" {
					lines = append(lines, p[0]+" "+p[1])
				}
			}
			return note{title: "🆕 Neuer Drucker wartet auf Freigabe", body: strings.Join(lines, "\n"), kind: "info"}, true
		}},
	{key: "upload_failed", typ: "com.prusa.connect.device.transfer.settled", device: true,
		build: func(d json.RawMessage, dev string, _ *settings, _ *app) (note, bool) {
			var t struct {
				State      string `json:"state"`
				BytesSent  int64  `json:"bytes_sent"`
				BytesTotal int64  `json:"bytes_total"`
				Reason     string `json:"reason"`
			}
			if json.Unmarshal(d, &t) != nil || t.State == "done" || t.State == "" {
				return note{}, false
			}
			word := map[string]string{"failed": "fehlgeschlagen", "cancelled": "abgebrochen", "interrupted": "unterbrochen"}[t.State]
			if word == "" {
				word = t.State
			}
			var lines []string
			if t.Reason != "" {
				lines = append(lines, t.Reason)
			}
			if t.BytesTotal > 0 {
				lines = append(lines, fmt.Sprintf("%.1f von %.1f MB übertragen", float64(t.BytesSent)/1e6, float64(t.BytesTotal)/1e6))
			}
			return note{title: "📤 " + dev + ": Übertragung " + word, body: strings.Join(lines, "\n"), kind: "warning"}, true
		}},
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func specByKey(k string) *spec {
	for i := range specs {
		if specs[i].key == k {
			return &specs[i]
		}
	}
	return nil
}

func specByType(t string) *spec {
	for i := range specs {
		if specs[i].typ == t {
			return &specs[i]
		}
	}
	return nil
}

// buildIssues meldet nur, wenn sich die Liste der Probleme geändert hat und nicht leer ist: der Host sendet
// bei jeder Änderung die ganze Liste.
func buildIssues(d json.RawMessage, dev string, _ *settings, a *app) (note, bool) {
	var x struct {
		Issues []struct {
			Reason   string `json:"reason"`
			Severity string `json:"severity"`
			Detail   string `json:"detail"`
		} `json:"issues"`
		Dialog *struct {
			Title string `json:"title"`
			Text  string `json:"text"`
		} `json:"dialog"`
	}
	if json.Unmarshal(d, &x) != nil {
		return note{}, false
	}
	var sig strings.Builder
	var lines []string
	worst := "info"
	for _, i := range x.Issues {
		sig.WriteString(i.Reason + "|" + i.Severity + ";")
		l := i.Reason
		if i.Detail != "" {
			l += ": " + i.Detail
		}
		lines = append(lines, l)
		if i.Severity == "error" {
			worst = "failure"
		} else if i.Severity == "warning" && worst != "failure" {
			worst = "warning"
		}
	}
	if x.Dialog != nil && (x.Dialog.Title != "" || x.Dialog.Text != "") {
		sig.WriteString("dialog|" + x.Dialog.Title)
		lines = append(lines, "Am Gerät: "+strings.TrimSpace(x.Dialog.Title+" "+x.Dialog.Text))
	}
	if a.issueSig[dev] == sig.String() {
		return note{}, false
	}
	a.issueSig[dev] = sig.String()
	if sig.Len() == 0 {
		return note{}, false
	}
	return note{title: "⚠️ " + dev + ": Hinweis", body: strings.Join(lines, "\n"), kind: worst}, true
}
