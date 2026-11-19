# apprise-sink

A small service module for [Prusa Connect](https://github.com/prusa3d/prusa-connect) (Connect Local) that reports print events to an [Apprise](https://github.com/caronc/apprise) server, so that you get a message on Signal, Telegram, e-mail or any other target Apprise knows.

It is a community project and is not made by or affiliated with Prusa Research. It was written against the Connect Local closed beta (0.1.0-beta.1).

## What it reports

A message for each of these events (you choose which ones):

| Event | Message |
|---|---|
| `job_started` | "Druck gestartet" with the printer, the file and the start time |
| `job_settled` | finished, failed, cancelled or unknown, with the file, the print time, the filament used and the end time |
| `printer_online`, `printer_offline`, `printer_issues`, `new_printer`, `upload_failed` | optional, off by default |

The text of the messages is English by default. Set the option `language` to `de` for German.

## How it works

The module is a process the Connect Local server starts. It subscribes to the server's event stream (`bus.subscribe`), reads the printer's name with `device.get`, and posts a message to the Apprise notify address you give it. It speaks the module wire protocol itself and uses only the Go standard library; there is no dependency on the SDK. It keeps one small file in its data directory, the cursor (the position in the event stream), so that no event is sent twice after a restart.

## Install

From a release directory (module.yaml, the binary and checksums.txt):

```
prusa-connect module add github.com/django1982/apprise-sink@0.5.0 -from <release directory> -approve
```

Then restart the server so that it reads the new record. In a container that is `docker exec <container> /prusa_connect_local module add ...` (the binary name may differ in your image) and `docker restart <container>`. Without `-approve` the module is installed switched off and the command prints what it asks for.

## Settings

| Setting | Meaning |
|---|---|
| `apprise_url` | Full address of the Apprise notify endpoint, for example `http://apprise:8000/notify/KEY` |
| `apprise_user`, `apprise_password` | HTTP basic authentication, empty for none. The password is a secret setting and is never logged |
| `apprise_tag` | Apprise tag that selects the targets (comma means or, space means and). Empty sends to all |
| `language` | Language of the notification texts: `en` (English, default) or `de` (German) |
| `device_names` | Fallback names from device id to name, used only when the server cannot tell a name |
| `events` | Which events to report (see above) |
| `job_outcomes` | Which print results to report: `finished`, `failed`, `cancelled`, `unknown` |
| `debug` | Write a detailed log file to the module's data directory. Turning it off deletes the file. No credentials are written |
| `dry_run` | Only log what would be sent and send nothing. **On by default** |

## Security

- The module sends only to the address in `apprise_url`. That is declared in `module.yaml` under `egress`, so the install dialog of Connect Local shows it.
- What a message contains: printer name, file name, result, duration, filament used and end time of a print.
- Permission: `observe` (reads events), no write access to printers or files.
- Messages travel in plain text if your Apprise address uses `http`. Use `https` or keep the Apprise server on a private network.

## Build and test

```
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o apprise-sink_linux_amd64 .
go vet ./...
go test ./...
```

## License

MIT, see [LICENSE](LICENSE). The binary contains the Go standard library, which is under the Go license (BSD 3-Clause, https://go.dev/LICENSE).
