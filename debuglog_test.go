package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDebugLog(t *testing.T) {
	dir := t.TempDir()
	var d debugLog
	d.set(dir, false)
	d.write("nix")
	if _, err := os.Stat(filepath.Join(dir, debugFile)); err == nil {
		t.Fatal("aus: es darf keine Datei entstehen")
	}
	d.set(dir, true)
	d.write("hallo")
	b, _ := os.ReadFile(filepath.Join(dir, debugFile))
	if !strings.Contains(string(b), "hallo") {
		t.Fatalf("Zeile fehlt: %q", b)
	}
	d.write(strings.Repeat("x", debugMax+10))
	d.write("danach")
	if _, err := os.Stat(filepath.Join(dir, debugFile+".1")); err != nil {
		t.Fatal("Rotation fehlt")
	}
	d.set(dir, false)
	for _, f := range []string{debugFile, debugFile + ".1"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Fatalf("%s nicht gelöscht", f)
		}
	}
}
