# ipm-baseii

Inspect, validate, and export Mastercard IPM and Visa BASE II clearing files.

ipm-baseii provides a local web dashboard and four command-line tools. Browse
records, review validation findings, compare totals by currency and transaction
flow, and download CSV or text reports.

Written in Go, with no external Go dependencies. Templates, styles, scripts,
and icons are embedded in the dashboard binary. No CDN or frontend build is
needed.

## Requirements

- Go 1.22 or newer.
- A browser for the dashboard.
- A clearing file in a supported format.

No database, Node.js, Docker, or API keys are required. Run these commands
from the repository root. The internal Go module name remains `mciipm`.

## Quick start

After cloning the repository, enter its directory and start the dashboard:

```sh
cd ipm-baseii
go run ./cmd/ipmweb
```

Open [localhost:8080](http://localhost:8080), choose Mastercard or Visa, select
the file options, and upload a file. Automatic encoding detection is the
default. For Visa, select CTF or ITF; framing is detected automatically unless
overridden.

The dashboard supports record details, severity filters, pagination, Mastercard
originator/destination validation, and CSV and report downloads. Restarting the
server clears all sessions. Sessions idle for more than two hours are removed
by a background sweep; the browser cookie also expires after two hours.

Use another local port with:

```sh
go run ./cmd/ipmweb -addr localhost:8081
```

## Build

Build all five executables into `bin/`:

```sh
go build -o ./bin/ ./cmd/...
```

Go adds `.exe` on Windows. Launch the built dashboard with `./bin/ipmweb` on
Linux/macOS or `.\bin\ipmweb.exe` in PowerShell.

| Command | Purpose |
| --- | --- |
| `ipmweb` | Local dashboard for both schemes |
| `ipm2csv` | Mastercard IPM to CSV |
| `ipmvalidate` | Mastercard validation and text report |
| `visa2csv` | Visa BASE II to CSV |
| `visavalidate` | Visa validation and text report |

The examples below use `go run` so they work without building executables
first. Replace `go run ./cmd/<command>` with the built executable when needed.
Use `-h` with any command to see its flags.

## Mastercard IPM

Convert presentments, financial detail addenda, and chargebacks:

```sh
go run ./cmd/ipm2csv -in input.ipm -out output.csv
```

Validate a file you received and save a report:

```sh
go run ./cmd/ipmvalidate -in input.ipm -role dst -report validation.txt
```

For IBM 1014-byte blocking, add `-blocked`:

```sh
go run ./cmd/ipm2csv -in input.ipm -out output.csv -blocked -encoding ebcdic
```

| Flag | Commands | Default | Meaning |
| --- | --- | --- | --- |
| `-in` | Both | Required | Input file path |
| `-out` | `ipm2csv` | Required | Output CSV path |
| `-report` | `ipmvalidate` | Standard output | Text report path |
| `-encoding` | Both | `auto` | `auto`, `ascii`, or CP037 `ebcdic` |
| `-blocked` | Both | `false` | Strip 1014-byte blocks before VBS parsing |
| `-include-all` | `ipm2csv` | `false` | Include parsed file-control and other messages |
| `-role` | `ipmvalidate` | `org` | `org`: sending file; `dst`: received file |
| `-max-findings` | `ipmvalidate` | `500` | Listed findings; `0` lists all |

CSV output has one row per included message. Columns are the union of fields
found in those messages: `MTI`, numerically sorted `DE` tags, then numerically
sorted `PDS` tags. Missing values are empty. PDS containers in DE48, DE62,
DE123, DE124, and DE125 expand into tags such as `PDS0501`. DE55 binary
values are hex-encoded.

Validation checks parsing and field bounds, the secondary-bitmap flag, known
MTI/function-code combinations, mandatory fields for the selected role,
numeric fields, selected dates, and PAN Luhn checks. Whole-file checks
reconcile PDS0306 message counts and PDS0301 amount checksums, check DE71
sequence/reset rules, and check file header/trailer structure.

## Visa BASE II

Convert a Center Transaction File (CTF):

```sh
go run ./cmd/visa2csv -in input.ctf -out output.csv
```

Validate an Interchange Transaction File (ITF):

```sh
go run ./cmd/visavalidate -in input.itf -format itf -report validation.txt
```

Override framing for a line-delimited export when needed:

```sh
go run ./cmd/visa2csv -in input.ctf -out output.csv -framing lines
```

| Flag | Commands | Default | Meaning |
| --- | --- | --- | --- |
| `-in` | Both | Required | Input file path |
| `-out` | `visa2csv` | Required | Output CSV path |
| `-report` | `visavalidate` | Standard output | Text report path |
| `-encoding` | Both | `auto` | `auto`, `ascii`, or CP037 `ebcdic` |
| `-format` | Both | `ctf` | `ctf`: 168 bytes; `itf`: 170 bytes |
| `-framing` | Both | `auto` | `fixed`: consecutive records; `lines`: LF/CRLF |
| `-include-all` | `visa2csv` | `false` | Include parsed control/other records |
| `-max-findings` | `visavalidate` | `500` | Listed findings; `0` lists all |

Supported draft-data transaction codes are 05/06/07/15/16/17/25/26/27/35/36/37.
Consecutive TCRs are grouped by transaction code and ascending component
sequence. CSV output has one row per transaction, with `TransactionCode`
followed by sorted tags such as `TCR0.AccountNumber` and
`TCR1.MemberMessageText`.

Validation checks supported draft record lengths, recognized TCR indicators,
layout variants, numeric fields, account-number check digits, and selected
dates. Numeric fields are checked for decoded TC90/91/92 control records.
Batch/file trailer checks reconcile transaction counts, TCR counts, source
amounts, and destination amounts. Monetary-transaction count mismatches are
warnings: this implementation counts the draft-data family as monetary.

## Reading results

- Validators exit nonzero when they find errors. Warnings alone do not cause
  validation failure. Limiting listed findings does not change the totals.
- Converters skip records with parse errors and print warnings to standard
  error. Conversion is not a substitute for validation.
- A truncated Visa scan can leave a partial CSV. The converter reports the
  scan failure and exits nonzero; inspect the warning before using its output.
- Dashboard uploads that stop partway through a scan display a warning.
  Counts and exports then describe only the records read so far.
- Amounts are grouped by currency and transaction flow. Debits and credits
  are not netted. Mastercard PDS0148 currency exponents take precedence over
  the fallback currency table; unknown currencies default to two decimals.
- Zero-filled control totals are treated as unpopulated. Excerpts without
  file controls/trailers skip the applicable file-structure checks.

## Data handling

The dashboard binds to `localhost:8080` by default and has no login or TLS.
It is intended for local use by one operator. Binding it to a shared network
requires access controls outside this application.

Uploads are limited to 500 MiB per request and three million Mastercard
records or Visa transactions. These are input caps, not memory guarantees:
parsing, findings, and cached rows can use substantially more RAM than the
uploaded file. CLI tools also retain parsed records in memory.

Parsed sessions live in memory. Multipart uploads can spill to the OS temporary
directory and are removed after handling. Dashboard pages use `no-store`;
static assets are cached. Session cookies are HttpOnly and SameSite=Lax.

Known PAN-bearing fields and card-number-length runs in finding messages are
masked in dashboard views. CSV exports retain full field values; reports can
quote invalid field values. Treat inputs, exports, reports, and temporary
storage as sensitive. Display masking is not storage encryption or a claim of
[PCI DSS compliance](https://www.pcisecuritystandards.org/faqs/1492/).

CSV fields beginning with `=`, `+`, `-`, `@`, tab, or carriage return receive
a leading apostrophe to reduce spreadsheet formula execution. This changes
the exported value; preserve the source file when exact bytes matter.

Local input formats, export directories, CSV files, build output, and
assistant/editor settings are ignored by Git. Tests generate synthetic records;
no production clearing files are needed.

## Standards coverage and limitations

The catalogs were originally documented as derived from these editions:

- Mastercard *IPM Clearing Formats*, 15 October 2019 (Chapters 2, 3, and 7).
- Visa *BASE II Clearing Interchange Formats, TC 01 to TC 49*, 19 October 2019.
- Visa *BASE II Clearing Interchange Formats, TC 50 to TC 92*, 13 April 2019.

Those source manuals and the original extraction scripts are not included.
The checked-in catalogs define this implementation's coverage. Passing its
tests does not establish compatibility with newer network specifications or
network certification.

Known limits:

- Mastercard conditional-field requirements are not evaluated. PDS values
  remain whole strings; most PDS subfields are not decoded or validated.
- Duplicate PDS tags within one container keep the last value. Duplicate tags
  across containers also keep the later value and generate a warning.
- Visa TCR2 country variants require external country context and are not
  automatically resolved. Unclassified variants produce warnings.
- Visa TC90 uses the incoming control layout; direction cannot be inferred
  from the record alone. Short line-delimited control records are tolerated.
- Other Visa transaction-code families have no detailed field validation.
  ITF hash bytes are stripped, not verified.
- Automatic encoding/framing detection is heuristic. Override it when the
  input's transport format is known.
- Currency display uses a built-in fallback table, not a live currency feed.
  CVE and VES use two decimals, consistent with the
  [Mastercard currency table](https://www.mastercard.bg/content/dam/mccom/global/documents/quick-reference-booklet-merchant.pdf)
  and [ISO 4217 amendment 168](https://www.currency-iso.org/dam/downloads/dl_currency_iso_amendment_168.pdf).

## Development

```sh
go test ./...
go vet ./...
go build ./...
```

Format Go changes with `gofmt`. Tests cover synthetic ASCII/EBCDIC records,
framing, parsing, CSV output, validation, control totals, currency formatting,
and HTTP dashboard flows. With race-detector support, also run
`go test -race ./...`.

| Directory | Contents |
| --- | --- |
| `cmd/` | Dashboard and CLI entry points |
| `internal/catalog/` | Embedded Mastercard field and message catalogs |
| `internal/iso8583/` | Mastercard bitmap, field, and PDS parsing |
| `internal/vbs/` | Mastercard VBS and 1014-byte framing |
| `internal/convert/`, `internal/validate/` | Mastercard CSV, validation, reports |
| `internal/baseii/` | Visa framing, grouping, parsing, CSV, validation |
| `internal/money/`, `internal/ebcdic/` | Currency formatting and CP037 codec |
| `internal/webapp/` | HTTP handlers, sessions, templates, static assets |
| `internal/integrationtest/` | Synthetic end-to-end tests |

For parser or validation changes, add a regression test using synthetic data
and run the checks above. Include the format edition and field/rule reference
when changing a catalog. Keep production files and cardholder data out of
issues and pull requests.

## Third-party assets and licensing

The dashboard includes htmx 1.9.12 under Zero-Clause BSD; see
[third-party notices](THIRD_PARTY_NOTICES.md). A project-wide license has not
been selected.
