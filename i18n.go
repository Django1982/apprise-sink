package main

import "strings"

// Sprachen der Nachrichten. Vorgabe ist Englisch, "de" schaltet auf Deutsch.
const (
	langEN = "en"
	langDE = "de"
)

// normLang macht aus der Einstellung language eine bekannte Sprache; alles Unbekannte ist Englisch.
func normLang(s string) string {
	if strings.EqualFold(strings.TrimSpace(s), langDE) {
		return langDE
	}
	return langEN
}

// msgs enthält die Texte je Sprache. Platzhalter: %s, %.0f, %.1f wie in fmt.
var msgs = map[string][2]string{ // {en, de}
	"printer":         {"Printer", "Drucker"},
	"job_started":     {"print started", "Druck gestartet"},
	"job_finished":    {"print finished", "Druck fertig"},
	"job_failed":      {"print failed", "Druck fehlgeschlagen"},
	"job_cancelled":   {"print cancelled", "Druck abgebrochen"},
	"job_ended":       {"print ended", "Druck beendet"},
	"started_at":      {"Started %s", "Start %s Uhr"},
	"ended_at":        {"Ended %s", "Ende %s Uhr"},
	"progress":        {"Progress %.0f %%", "Fortschritt %.0f %%"},
	"duration":        {"Duration %s", "Dauer %s"},
	"filament":        {"Filament %s", "Filament %s"},
	"result_unknown":  {"Result unknown, the device reports %s", "Ergebnis unbekannt, das Gerät meldet %s"},
	"online":          {"connected", "verbunden"},
	"offline":         {"disconnected", "getrennt"},
	"issues":          {"notice", "Hinweis"},
	"on_device":       {"On the device: ", "Am Gerät: "},
	"new_printer":     {"New printer waiting for approval", "Neuer Drucker wartet auf Freigabe"},
	"code":            {"Code", "Code"},
	"model":           {"Model", "Modell"},
	"serial":          {"Serial number", "Seriennummer"},
	"firmware":        {"Firmware", "Firmware"},
	"transfer":        {"transfer %s", "Übertragung %s"},
	"transfer_failed": {"failed", "fehlgeschlagen"},
	"transfer_cancel": {"cancelled", "abgebrochen"},
	"transfer_interr": {"interrupted", "unterbrochen"},
	"transferred":     {"%.1f of %.1f MB transferred", "%.1f von %.1f MB übertragen"},
}

// tr liefert den Text zum Schlüssel in der Sprache.
func tr(lang, key string) string {
	m := msgs[key]
	if lang == langDE {
		return m[1]
	}
	return m[0]
}
