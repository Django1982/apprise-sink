package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// note ist eine fertige Nachricht. kind ist der Apprise-Typ: info, success, warning, failure.
type note struct {
	title, body, kind string
}

type jobData struct {
	JobID         string  `json:"job_id"`
	DisplayName   string  `json:"display_name"`
	Path          string  `json:"path"`
	Started       string  `json:"started"`
	Ended         string  `json:"ended"`
	Outcome       string  `json:"outcome"`
	MachineState  string  `json:"machine_state"`
	Progress      float64 `json:"progress"`
	TimePrintingS int64   `json:"time_printing_s"`
	TimeKnown     bool    `json:"time_printing_known"`
	FilamentMM    float64 `json:"filament_used_mm"`
	FilamentKnown bool    `json:"filament_used_known"`
}

func deviceName(names map[string]string, id, lang string) string {
	if n := names[id]; n != "" {
		return n
	}
	return tr(lang, "printer")
}

func duration(s int64) string {
	switch {
	case s < 60:
		return fmt.Sprintf("%d s", s)
	case s < 3600:
		return fmt.Sprintf("%d min %02d s", s/60, s%60)
	default:
		return fmt.Sprintf("%d h %02d min", s/3600, (s%3600)/60)
	}
}

func filament(mm float64, lang string) string {
	if mm >= 1000 {
		v := fmt.Sprintf("%.2f m", mm/1000)
		if lang == langDE {
			v = strings.Replace(v, ".", ",", 1)
		}
		return v
	}
	return fmt.Sprintf("%.0f mm", mm)
}

// clock rechnet den Zeitstempel in die Ortszeit des Prozesses um (time.Local, im Container
// aus dem eingebundenen /etc/localtime; das Scratch-Image hat kein tzdata). Ungültig: leer.
func clock(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339Nano, rfc3339)
	if err != nil {
		return ""
	}
	return t.Local().Format("15:04")
}

func fileName(d jobData) string {
	if d.DisplayName != "" {
		return d.DisplayName
	}
	return d.Path
}

// build macht aus einem Ereignis die Nachricht. ok ist false für Typen, die wir nicht melden.
func build(eventType string, raw json.RawMessage, dev, lang string) (note, bool) {
	var d jobData
	if err := json.Unmarshal(raw, &d); err != nil {
		return note{}, false
	}
	var lines []string
	add := func(s string) {
		if s != "" {
			lines = append(lines, s)
		}
	}
	add(fileName(d))
	switch eventType {
	case "com.prusa.connect.device.job.started":
		if t := clock(d.Started); t != "" {
			add(fmt.Sprintf(tr(lang, "started_at"), t))
		}
		return note{title: "🖨️ " + dev + ": " + tr(lang, "job_started"), body: strings.Join(lines, "\n"), kind: "info"}, true
	case "com.prusa.connect.device.job.settled":
		var title, kind string
		switch d.Outcome {
		case "finished":
			title, kind = "✅ "+dev+": "+tr(lang, "job_finished"), "success"
		case "failed":
			title, kind = "❌ "+dev+": "+tr(lang, "job_failed"), "failure"
		case "cancelled":
			title, kind = "⏹️ "+dev+": "+tr(lang, "job_cancelled"), "warning"
		default:
			title, kind = "❔ "+dev+": "+tr(lang, "job_ended"), "info"
		}
		if d.Outcome == "failed" || d.Outcome == "cancelled" {
			add(fmt.Sprintf(tr(lang, "progress"), d.Progress))
		}
		if d.TimeKnown {
			add(fmt.Sprintf(tr(lang, "duration"), duration(d.TimePrintingS)))
		}
		if d.FilamentKnown && d.FilamentMM > 0 {
			add(fmt.Sprintf(tr(lang, "filament"), filament(d.FilamentMM, lang)))
		}
		if t := clock(d.Ended); t != "" {
			add(fmt.Sprintf(tr(lang, "ended_at"), t))
		}
		if d.Outcome != "finished" && d.Outcome != "failed" && d.Outcome != "cancelled" && d.MachineState != "" {
			add(fmt.Sprintf(tr(lang, "result_unknown"), d.MachineState))
		}
		return note{title: title, body: strings.Join(lines, "\n"), kind: kind}, true
	}
	return note{}, false
}

// send schickt eine Nachricht an die Apprise-API. Zugangsdaten stehen nie im Log.
// errUnsicher: Antwort blieb aus, ob zugestellt wurde ist offen.
var errUnsicher = errors.New("keine Antwort von Apprise (Zustellung unklar, keine Wiederholung)")

func send(client *http.Client, s settings, n note) error {
	payload := map[string]string{"title": n.title, "body": n.body, "type": n.kind}
	if s.Tag != "" {
		payload["tag"] = s.Tag
	}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, s.URL, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("Adresse nicht lesbar")
	}
	req.Header.Set("Content-Type", "application/json")
	if s.User != "" {
		req.SetBasicAuth(s.User, s.Password)
	}
	resp, err := client.Do(req)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			// Apprise kann trotz Timeout schon zugestellt haben: nicht wiederholen, sonst doppelt.
			return errUnsicher
		}
		return fmt.Errorf("Anfrage fehlgeschlagen")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		dlog("Apprise HTTP %d, Antwort: %s", resp.StatusCode, clip(string(body), 300))
		return fmt.Errorf("Apprise antwortet HTTP %d", resp.StatusCode)
	}
	return nil
}
