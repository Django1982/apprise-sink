// Command apprise-sink schickt Druck-Ereignisse von Connect Local an eine Apprise-API.
// Gebaut ohne SDK (nicht veröffentlicht): nur die Standardbibliothek, das Wire-Protokoll aus der Doku.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	name    = "apprise-sink"
	release = "0.4.2"

	subWindow  = 1 << 16
	cursorFile = "cursor"
)

// flexBool und flexMap nehmen den Wert so an, wie der Host ihn liefert: als JSON-Wert oder als Text.
type flexBool bool

func (b *flexBool) UnmarshalJSON(p []byte) error {
	var v any
	if err := json.Unmarshal(p, &v); err != nil {
		return err
	}
	switch x := v.(type) {
	case bool:
		*b = flexBool(x)
	case string:
		*b = flexBool(x == "true")
	default:
		return fmt.Errorf("not a bool")
	}
	return nil
}

type flexMap map[string]string

func (m *flexMap) UnmarshalJSON(p []byte) error {
	var s string
	if json.Unmarshal(p, &s) == nil {
		if s == "" {
			*m = flexMap{}
			return nil
		}
		p = []byte(s)
	}
	var mm map[string]string
	if err := json.Unmarshal(p, &mm); err != nil {
		return err
	}
	*m = mm
	return nil
}

type flexList []string

func (l *flexList) UnmarshalJSON(p []byte) error {
	var s string
	if json.Unmarshal(p, &s) == nil {
		if s == "" {
			*l = nil
			return nil
		}
		p = []byte(s)
	}
	var ll []string
	if err := json.Unmarshal(p, &ll); err != nil {
		return err
	}
	*l = ll
	return nil
}

type settings struct {
	URL         string   `json:"apprise_url"`
	User        string   `json:"apprise_user"`
	Password    string   `json:"apprise_password"`
	Tag         string   `json:"apprise_tag"`
	DeviceNames flexMap  `json:"device_names"`
	DryRun      flexBool `json:"dry_run"`
	Debug       flexBool `json:"debug"`
	Events      flexList `json:"events"`
	Outcomes    flexList `json:"job_outcomes"`
}

type app struct {
	ch       *channel
	nextSID  atomic.Uint32
	subs     sync.Map // sid -> *subStream
	dataDir  string
	lines    chan []byte
	types    []string
	issueSig map[string]string
	calls    sync.Map // sid -> chan frame
	resub    chan struct{}
	started  atomic.Bool
	cfg      atomic.Pointer[settings]
	out      chan note
	seen     []string
	client   *http.Client
}

type subStream struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (a *app) answer(sid uint32, id json.RawMessage, result any, e *rpcErr) {
	m := rpcMsg{JSONRPC: "2.0", ID: id}
	if e != nil {
		m.Error = e
	} else {
		b, _ := json.Marshal(result)
		m.Result = b
	}
	b, _ := json.Marshal(m)
	if err := a.ch.write(sid, kindControl, b); err != nil {
		logf("answer: %v", err)
		return
	}
	_ = a.ch.write(sid, kindEnd, nil)
}

// call ruft eine Methode des Hosts auf, etwa device.get.
func (a *app) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	sid := a.nextSID.Add(2)
	ch := make(chan frame, 4)
	a.calls.Store(sid, ch)
	defer a.calls.Delete(sid)
	p, _ := json.Marshal(params)
	req, _ := json.Marshal(rpcMsg{JSONRPC: "2.0", Method: method, Params: p, ID: json.RawMessage(`1`)})
	if err := a.ch.write(sid, kindControl, req); err != nil {
		return nil, err
	}
	_ = a.ch.write(sid, kindEnd, nil)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		select {
		case f := <-ch:
			switch f.kind {
			case kindControl:
				var m rpcMsg
				if err := json.Unmarshal(f.payload, &m); err != nil {
					return nil, err
				}
				if m.Error != nil {
					return nil, m.Error
				}
				return m.Result, nil
			case kindReset:
				return nil, errors.New("stream reset")
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// resolveName fragt den Server nach dem Namen des Druckers: erst ein vom Nutzer vergebener Gerätename,
// dann der Hostname, den der Drucker selbst meldet, zuletzt die Einstellung device_names.
func (a *app) resolveName(ctx context.Context, id string, fallback map[string]string) string {
	res, err := a.call(ctx, "device.get", map[string]string{"id": id})
	if err == nil {
		var d struct {
			Name   string                     `json:"name"`
			Model  string                     `json:"model"`
			Serial string                     `json:"serial"`
			State  map[string]json.RawMessage `json:"state"`
		}
		if json.Unmarshal(res, &d) == nil {
			if d.Name != "" && d.Name != strings.TrimSpace(d.Model+" "+d.Serial) {
				return d.Name
			}
			var host string
			if json.Unmarshal(d.State["/network/hostname"], &host) == nil && host != "" {
				return host
			}
			if d.Name != "" {
				return d.Name
			}
		}
	} else {
		logf("device.get: %v", err)
	}
	return deviceName(fallback, id)
}

// Der Cursor liegt als Datei im Datenverzeichnis, das der Host dem Modul gibt (needs: data).
func (a *app) cursorFile() string {
	if a.dataDir == "" {
		return ""
	}
	return filepath.Join(a.dataDir, cursorFile)
}

func (a *app) loadCursor() string {
	f := a.cursorFile()
	if f == "" {
		return ""
	}
	b, err := os.ReadFile(f)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (a *app) saveCursor(c string) {
	f := a.cursorFile()
	if f == "" || c == "" {
		return
	}
	tmp := f + ".tmp"
	if err := os.WriteFile(tmp, []byte(c), 0o600); err != nil {
		logf("Cursor nicht gespeichert: %v", err)
		return
	}
	if err := os.Rename(tmp, f); err != nil {
		logf("Cursor nicht gespeichert: %v", err)
	}
}

func (a *app) handle(ctx context.Context, f frame, stop func()) {
	var m rpcMsg
	if err := json.Unmarshal(f.payload, &m); err != nil || m.Method == "" {
		logf("stream %d: control frame is not a request", f.sid)
		return
	}
	switch m.Method {
	case "plugin.info":
		a.answer(f.sid, m.ID, map[string]any{
			"name": name, "version": release, "contract": "1.0.0",
			"serves": []string{"plugin.configure"},
		}, nil)
	case "plugin.configure":
		var p struct {
			Config json.RawMessage `json:"config"`
			Grants *struct {
				Data string `json:"data"`
			} `json:"grants"`
		}
		_ = json.Unmarshal(m.Params, &p)
		var s settings
		dec := json.NewDecoder(bytes.NewReader(p.Config))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&s); err != nil {
			a.answer(f.sid, m.ID, nil, &rpcErr{Code: -32602, Message: "settings not readable: " + err.Error()})
			return
		}
		if !bool(s.DryRun) && s.URL == "" {
			a.answer(f.sid, m.ID, nil, &rpcErr{Code: -32602, Message: "apprise_url is required unless dry_run is on"})
			return
		}
		var types []string
		for _, k := range s.Events {
			sp := specByKey(k)
			if sp == nil {
				a.answer(f.sid, m.ID, nil, &rpcErr{Code: -32602, Message: "unknown event " + k})
				return
			}
			types = append(types, sp.typ)
		}
		for _, o := range s.Outcomes {
			if o != "finished" && o != "failed" && o != "cancelled" && o != "unknown" {
				a.answer(f.sid, m.ID, nil, &rpcErr{Code: -32602, Message: "unknown job outcome " + o})
				return
			}
		}
		a.types = types
		a.cfg.Store(&s)
		if p.Grants != nil {
			a.dataDir = p.Grants.Data
		}
		if a.dataDir == "" {
			logf("kein Datenverzeichnis: der Cursor wird nicht gemerkt, nach einem Neustart fehlen Ereignisse aus der Pause")
		}
		dbg.set(a.dataDir, bool(s.Debug))
		dlog("Debug-Datei an, Version %s, Datei %s (Zugangsdaten werden nie geschrieben)", release, dbg.path)
		var names []string
		for k := range s.DeviceNames {
			names = append(names, k)
		}
		sort.Strings(names)
		logf("configure: dry_run=%v, apprise_url gesetzt=%v, Zugang gesetzt=%v, Tag=%q, Geräte=%d, Ereignisse=%v, Ergebnisse=%v, Debug=%v", bool(s.DryRun), s.URL != "", s.User != "", s.Tag, len(names), []string(s.Events), []string(s.Outcomes), bool(s.Debug))
		a.answer(f.sid, m.ID, nil, nil)
		if len(a.types) == 0 {
			logf("keine Ereignisse eingeschaltet, nichts wird abonniert")
		}
		if a.started.CompareAndSwap(false, true) && len(a.types) > 0 {
			go a.sender(ctx)
			go a.worker(ctx)
			go a.subscribeLoop(ctx)
		}
	case "plugin.shutdown":
		logf("shutdown")
		a.answer(f.sid, m.ID, nil, nil)
		go func() { time.Sleep(200 * time.Millisecond); stop() }()
	default:
		a.answer(f.sid, m.ID, nil, &rpcErr{Code: -32601, Message: "method not found"})
	}
}

func (a *app) subscribeLoop(ctx context.Context) {
	for ctx.Err() == nil {
		cursor := a.loadCursor()
		sid := a.nextSID.Add(2)
		a.subs.Store(sid, &subStream{})
		params := map[string]any{"types": a.types, "interval_ms": 1000}
		if cursor != "" {
			params["resume"] = cursor
		}
		p, _ := json.Marshal(params)
		req, _ := json.Marshal(rpcMsg{JSONRPC: "2.0", Method: "bus.subscribe", Params: p, ID: json.RawMessage(`1`)})
		if err := a.ch.credit(sid, subWindow); err != nil {
			logf("subscribe credit: %v", err)
			return
		}
		if err := a.ch.write(sid, kindControl, req); err != nil {
			logf("subscribe: %v", err)
			return
		}
		logf("subscribe: Stream %d offen, fortgesetzt=%v", sid, cursor != "")
		select {
		case <-a.resub:
		case <-ctx.Done():
			return
		}
		a.subs.Delete(sid)
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return
		}
	}
}

func (a *app) signalResub() {
	select {
	case a.resub <- struct{}{}:
	default:
	}
}

// onSub verarbeitet einen Frame auf einem Stream, den wir selbst geöffnet haben.
func (a *app) onSub(ctx context.Context, sid uint32, st *subStream, f frame) {
	switch f.kind {
	case kindControl:
		var m rpcMsg
		if json.Unmarshal(f.payload, &m) == nil && m.Error != nil {
			logf("subscribe abgelehnt: %v", m.Error)
		}
	case kindData:
		if len(f.payload) > 0 {
			_ = a.ch.credit(sid, uint32(len(f.payload)))
		}
		st.mu.Lock()
		st.buf.Write(f.payload)
		var lines [][]byte
		for {
			i := bytes.IndexByte(st.buf.Bytes(), '\n')
			if i < 0 {
				break
			}
			line := append([]byte(nil), st.buf.Next(i + 1)[:i]...)
			lines = append(lines, line)
		}
		st.mu.Unlock()
		// Nie in der Leseschleife arbeiten: window ruft den Host an, und dessen Antwort liest genau diese Schleife.
		for _, l := range lines {
			select {
			case a.lines <- l:
			default:
				logf("Fenster-Warteschlange voll, ein Fenster verworfen")
			}
		}
	case kindEnd:
		logf("subscribe: Host beendet Stream %d", sid)
		a.signalResub()
	case kindReset:
		logf("subscribe: Stream %d zurückgesetzt: %s", sid, string(f.payload))
		a.signalResub()
	}
}

func (a *app) window(ctx context.Context, line []byte) {
	var w struct {
		Events []json.RawMessage `json:"events"`
		Cursor string            `json:"cursor"`
		Resync bool              `json:"resync"`
	}
	if err := json.Unmarshal(line, &w); err != nil {
		logf("Fenster nicht lesbar: %v", err)
		return
	}
	if w.Resync {
		logf("Resync: Kontinuität verloren, Ereignisse können fehlen")
	}
	cfg := a.cfg.Load()
	for _, raw := range w.Events {
		var env struct {
			ID      string          `json:"id"`
			Type    string          `json:"type"`
			Subject string          `json:"subject"`
			Data    json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			continue
		}
		if a.duplicate(env.ID) {
			continue
		}
		sp := specByType(env.Type)
		if sp == nil || !contains(a.types, env.Type) {
			continue
		}
		if sp.raw || sp.job {
			logf("roh %s id=%s: %s", env.Type, env.ID, string(env.Data))
		}
		if sp.job {
			// Der Host meldet dasselbe Jobende mehrfach neu (beobachtet 2026-11-05 nach Filamentwechsel).
			var j jobData
			if json.Unmarshal(env.Data, &j) == nil && (j.JobID != "" || j.Started != "") {
				if a.duplicate(env.Type + "|" + env.Subject + "|" + j.JobID + "|" + j.Started + "|" + j.Outcome) {
					logf("Job-Ereignis schon gemeldet, übersprungen: %s job=%s", env.Type, j.JobID)
					continue
				}
			}
		}
		dev := ""
		if sp.device {
			dev = a.resolveName(ctx, strings.TrimPrefix(env.Subject, "device:"), cfg.DeviceNames)
		}
		n, ok := sp.build(env.Data, dev, cfg, a)
		if !ok {
			continue
		}
		select {
		case a.out <- n:
		default:
			logf("Warteschlange voll, Nachricht verworfen: %s", n.title)
		}
	}
	a.saveCursor(w.Cursor)
}

func (a *app) duplicate(id string) bool {
	for _, s := range a.seen {
		if s == id {
			return true
		}
	}
	a.seen = append(a.seen, id)
	if len(a.seen) > 512 {
		a.seen = a.seen[len(a.seen)-512:]
	}
	return false
}

// worker verarbeitet die Fenster der Reihe nach, abseits der Leseschleife.
func (a *app) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case l := <-a.lines:
			a.window(ctx, l)
		}
	}
}

// sender liest die Warteschlange, damit der Bus nie auf Apprise wartet.
func (a *app) sender(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case n := <-a.out:
			cfg := a.cfg.Load()
			if bool(cfg.DryRun) {
				logf("dry_run, würde senden: %q | %q", n.title, n.body)
				continue
			}
			var err error
			for i, wait := range []time.Duration{0, 2 * time.Second, 5 * time.Second} {
				if wait > 0 {
					time.Sleep(wait)
				}
				t0 := time.Now()
				err = send(a.client, *cfg, n)
				dlog("Versand Versuch %d: %v nach %s", i+1, err, time.Since(t0).Round(time.Millisecond))
				if err == nil {
					break
				}
				logf("Versand Versuch %d: %v", i+1, err)
				if errors.Is(err, errUnsicher) {
					break
				}
			}
			if err != nil {
				logf("Nachricht verworfen: %q", n.title)
			} else {
				logf("gesendet: %q", n.title)
			}
		}
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	a := &app{
		ch:       &channel{r: bufio.NewReaderSize(os.Stdin, 64<<10), w: os.Stdout},
		resub:    make(chan struct{}, 1),
		out:      make(chan note, 64),
		issueSig: map[string]string{},
		lines:    make(chan []byte, 256),
		client:   &http.Client{Timeout: 45 * time.Second},
	}
	if _, err := os.Stdout.WriteString(`{"protocol":1,"min_minor":0,"max_minor":0,"address":"stdio"}` + "\n"); err != nil {
		return err
	}
	logf("gestartet %s", release)
	for {
		f, err := a.ch.read()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if v, ok := a.subs.Load(f.sid); ok {
			a.onSub(ctx, f.sid, v.(*subStream), f)
			continue
		}
		if v, ok := a.calls.Load(f.sid); ok {
			select {
			case v.(chan frame) <- f:
			default:
			}
			continue
		}
		if f.kind == kindControl {
			go a.handle(ctx, f, stop)
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		os.Exit(1)
	}
}
