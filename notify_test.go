package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBuild(t *testing.T) {
	cases := []struct{ typ, data, title, body string }{
		{"com.prusa.connect.device.job.started",
			`{"job_id":"x","display_name":"Gnome.bgcode","started":"2026-11-04T14:51:14.289806078+01:00"}`,
			"🖨️ sir-layerlot: Druck gestartet", "Gnome.bgcode\nStart 14:51 Uhr"},
		{"com.prusa.connect.device.job.settled",
			`{"display_name":"Gnome.bgcode","ended":"2026-11-04T14:51:48.9+01:00","outcome":"cancelled","machine_state":"STOPPED","progress":0,"time_printing_s":26,"time_printing_known":true}`,
			"⏹️ sir-layerlot: Druck abgebrochen", "Gnome.bgcode\nFortschritt 0 %\nDauer 26 s\nEnde 14:51 Uhr"},
		{"com.prusa.connect.device.job.settled",
			`{"display_name":"Gnome.bgcode","ended":"2026-11-04T17:02:10+01:00","outcome":"finished","progress":100,"time_printing_s":7800,"time_printing_known":true,"filament_used_mm":12400,"filament_used_known":true}`,
			"✅ sir-layerlot: Druck fertig", "Gnome.bgcode\nDauer 2 h 10 min\nFilament 12,40 m\nEnde 17:02 Uhr"},
	}
	for _, c := range cases {
		n, ok := build(c.typ, json.RawMessage(c.data), "sir-layerlot")
		if !ok || n.title != c.title || n.body != c.body {
			t.Errorf("%s: got %q | %q", c.typ, n.title, n.body)
		}
	}
	if _, ok := build("com.prusa.connect.device.other", json.RawMessage(`{}`), "x"); ok {
		t.Error("fremder Typ darf nicht gemeldet werden")
	}
	if deviceName(map[string]string{"a": "Eins"}, "b") != "Drucker" || deviceName(map[string]string{"a": "Eins"}, "a") != "Eins" {
		t.Error("Fallback-Name")
	}
}

func TestClock(t *testing.T) {
	old := time.Local
	time.Local = time.FixedZone("CET", 3600)
	defer func() { time.Local = old }()
	cases := []struct{ in, want string }{
		{"2026-11-18T11:30:02.876414801Z", "12:30"},
		{"2026-11-18T12:30:02+01:00", "12:30"},
		{"2026-11-18T14:30:02+03:00", "12:30"},
		{"2026-11-18T23:30:00Z", "00:30"},
		{"", ""},
		{"kein Stempel", ""},
		{"2026-11-18 11:30", ""},
	}
	for _, c := range cases {
		if got := clock(c.in); got != c.want {
			t.Errorf("clock(%q) = %q, erwartet %q", c.in, got, c.want)
		}
	}
	n, _ := build("com.prusa.connect.device.job.settled",
		json.RawMessage(`{"display_name":"a.bgcode","ended":"2026-11-18T11:30:02.876414801Z","outcome":"finished","time_printing_s":60,"time_printing_known":true}`), "d")
	if n.body != "a.bgcode\nDauer 1 min 00 s\nEnde 12:30 Uhr" {
		t.Errorf("body %q", n.body)
	}
}
