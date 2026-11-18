package main

import (
	"encoding/json"
	"testing"
)

func TestSpecs(t *testing.T) {
	cfg := &settings{Outcomes: flexList{"finished", "failed"}}
	a := &app{issueSig: map[string]string{}}
	settled := func(outcome string) (note, bool) {
		return specByKey("job_settled").build(json.RawMessage(`{"display_name":"x.bgcode","outcome":"`+outcome+`"}`), "p", cfg, a)
	}
	if _, ok := settled("cancelled"); ok {
		t.Error("cancelled ist ausgefiltert")
	}
	if n, ok := settled("finished"); !ok || n.kind != "success" {
		t.Errorf("finished: %v %v", n, ok)
	}
	if _, ok := settled("???"); ok {
		t.Error("unbekannt ist ausgefiltert (nicht in der Liste)")
	}
	up := specByKey("upload_failed").build
	if _, ok := up(json.RawMessage(`{"state":"done"}`), "p", cfg, a); ok {
		t.Error("done wird nicht gemeldet")
	}
	if n, ok := up(json.RawMessage(`{"state":"failed","bytes_sent":1000000,"bytes_total":4000000,"reason":"Timeout"}`), "p", cfg, a); !ok || n.title != "📤 p: Übertragung fehlgeschlagen" || n.body != "Timeout\n1.0 von 4.0 MB übertragen" {
		t.Errorf("upload: %q %q", n.title, n.body)
	}
	iss := specByKey("printer_issues").build
	d := json.RawMessage(`{"issues":[{"reason":"filament_runout","severity":"warning","detail":"Spule leer"}]}`)
	if n, ok := iss(d, "p", cfg, a); !ok || n.kind != "warning" {
		t.Errorf("issue: %v %v", n, ok)
	}
	if _, ok := iss(d, "p", cfg, a); ok {
		t.Error("gleiche Liste darf nicht noch einmal melden")
	}
	if _, ok := iss(json.RawMessage(`{"issues":[]}`), "p", cfg, a); ok {
		t.Error("leere Liste meldet nichts")
	}
	if n, ok := specByKey("new_printer").build(json.RawMessage(`{"code":"ABCD-1234","model":"COREONEL"}`), "", cfg, a); !ok || n.body != "Code ABCD-1234\nModell COREONEL" {
		t.Errorf("new_printer: %q", n.body)
	}
}

func TestFlexList(t *testing.T) {
	for _, in := range []string{`["a","b"]`, `"[\"a\",\"b\"]"`} {
		var l flexList
		if err := json.Unmarshal([]byte(in), &l); err != nil || len(l) != 2 {
			t.Errorf("%s: %v %v", in, l, err)
		}
	}
}
