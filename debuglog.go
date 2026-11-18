package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	debugFile = "debug.log"
	debugMax  = 2 << 20 // 2 MB, danach wird rotiert (eine Vorgängerdatei bleibt)
)

// debugLog schreibt auf Wunsch alles Relevante in eine Datei im Datenverzeichnis.
// Aus: es wird nichts geschrieben und vorhandene Dateien werden gelöscht.
// Zugangsdaten gehören nie in diese Datei, der Aufrufer reicht sie nicht herein.
type debugLog struct {
	mu   sync.Mutex
	path string
	on   bool
}

var dbg debugLog

func (d *debugLog) set(dir string, on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if dir == "" {
		d.on = false
		return
	}
	d.path = filepath.Join(dir, debugFile)
	if !on {
		_ = os.Remove(d.path)
		_ = os.Remove(d.path + ".1")
	}
	d.on = on
}

func (d *debugLog) write(line string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.on {
		return
	}
	if st, err := os.Stat(d.path); err == nil && st.Size() > debugMax {
		_ = os.Rename(d.path, d.path+".1")
	}
	f, err := os.OpenFile(d.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02T15:04:05.000-07:00"), line)
}

// dlog schreibt nur in die Debug-Datei, nicht ins Host-Log.
func dlog(format string, a ...any) { dbg.write(fmt.Sprintf(format, a...)) }

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
