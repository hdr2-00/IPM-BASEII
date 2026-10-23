package webapp

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mciipm/internal/baseii"
	"mciipm/internal/convert"
	"mciipm/internal/iso8583"
	"mciipm/internal/validate"
)

//go:embed templates
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

const (
	sessionCookieName = "mciipm_session"
	defaultPageSize   = 25
	// The strip collapses long files into this many bands; past roughly
	// this count each band is thinner than a pixel and tells you nothing.
	maxStripSegments    = 180
	defaultFindingsSize = 50
	maxUploadBytes      = 500 << 20 // 500MB
	maxRecordsPerUpload = 3_000_000 // safety cap independent of byte size
)

// Server holds the session store and parsed templates.
type Server struct {
	store *Store
	tmpl  *template.Template
	mux   *http.ServeMux
}

// assetVersion is a short digest of everything under static/, used to
// version asset URLs. The static handler caches aggressively (the files
// only change when the binary is rebuilt), but without a changing URL a
// rebuilt binary serves its new CSS to nobody who has already visited -
// their browser holds the old copy for a year.
func assetVersion() string {
	h := sha256.New()
	_ = fs.WalkDir(staticFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, readErr := staticFS.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		h.Write([]byte(path))
		h.Write(b)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// NewServer builds a ready-to-use Server with routes registered.
func NewServer() (*Server, error) {
	version := assetVersion()
	funcs := template.FuncMap{
		"assetURL": func(path string) string { return path + "?v=" + version },
		"add":      func(a, b int) int { return a + b },
		"sub":      func(a, b int) int { return a - b },
		"mul":      func(a, b int) int { return a * b },
		"min": func(a, b int) int {
			if a < b {
				return a
			}
			return b
		},
		"seq": func(a, b int) []int {
			var out []int
			for i := a; i <= b; i++ {
				out = append(out, i)
			}
			return out
		},
		"icon": iconHTML,
	}

	tmpl, err := template.New("").Funcs(funcs).ParseFS(templateFS,
		"templates/*.html", "templates/partials/*.html")
	if err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}

	s := &Server{
		store: NewStore(2 * time.Hour),
		tmpl:  tmpl,
		mux:   http.NewServeMux(),
	}
	s.routes()
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	staticSub, _ := fsSub(staticFS, "static")
	staticHandler := http.StripPrefix("/static/", http.FileServer(http.FS(staticSub)))
	s.mux.Handle("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Static content is embedded in the binary via go:embed and only
		// changes on rebuild, so it's safe to cache aggressively.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		staticHandler.ServeHTTP(w, r)
	}))

	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/upload", s.handleUpload)
	s.mux.HandleFunc("/dashboard", s.handleDashboard)
	s.mux.HandleFunc("/partial/overview", s.handleOverview)
	s.mux.HandleFunc("/partial/records", s.handleRecords)
	s.mux.HandleFunc("/partial/record", s.handleRecordDetail)
	s.mux.HandleFunc("/partial/findings", s.handleFindings)
	s.mux.HandleFunc("/revalidate", s.handleRevalidate)
	s.mux.HandleFunc("/export/csv", s.handleExportCSV)
	s.mux.HandleFunc("/export/report", s.handleExportReport)
}

// ---------------------------------------------------------------- pages ----

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.render(w, "index.html", map[string]any{"Title": "mciipm — Card Network Clearing File Dashboard"})
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sessionFromRequest(r)
	if !ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	sess.Lock()
	data := map[string]any{
		"Title":      "Dashboard — " + sess.Filename,
		"Session":    sess,
		"Scheme":     sess.Scheme,
		"Role":       sess.Role,
		"IncludeAll": sess.IncludeAll,
		"Summary":    currentGenericSummary(sess),
	}
	sess.Unlock()
	s.render(w, "dashboard.html", data)
}

// --------------------------------------------------------------- upload ----

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		s.renderUploadError(w, "Couldn't read the upload — the file may be too large (limit 500MB) or the request malformed.")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, header, err := r.FormFile("file")
	if err != nil {
		s.renderUploadError(w, "No file was selected. Choose a clearing file to analyze.")
		return
	}
	defer file.Close()

	raw, err := io.ReadAll(file)
	if err != nil {
		s.renderUploadError(w, "Couldn't read the uploaded file: "+err.Error())
		return
	}
	if len(raw) == 0 {
		s.renderUploadError(w, "The uploaded file is empty.")
		return
	}

	scheme := Scheme(r.FormValue("scheme"))
	if scheme != SchemeVisa {
		scheme = SchemeMastercard
	}
	includeAll := r.FormValue("include_all") == "on"

	sess := s.store.New()
	sess.Filename = header.Filename
	sess.Scheme = scheme
	sess.IncludeAll = includeAll

	if scheme == SchemeVisa {
		if err := s.populateVisaSession(sess, raw, r); err != nil {
			s.renderUploadError(w, err.Error())
			return
		}
	} else {
		if err := s.populateMastercardSession(sess, raw, r); err != nil {
			s.renderUploadError(w, err.Error())
			return
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sess.ID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((2 * time.Hour).Seconds()),
	})

	w.Header().Set("HX-Redirect", "/dashboard")
	w.WriteHeader(http.StatusOK)
}

func (s *Server) populateMastercardSession(sess *Session, raw []byte, r *http.Request) error {
	blocked := r.FormValue("blocked") == "on"
	roleParam := r.FormValue("role")
	role := validate.RoleOrg
	if roleParam == "dst" {
		role = validate.RoleDst
	}

	enc, err := resolveMastercardEncoding(raw, r.FormValue("encoding"), blocked)
	if err != nil {
		return err
	}

	opt := convert.Options{Encoding: enc, Blocked1014: blocked, IncludeAll: sess.IncludeAll, MaxRecords: maxRecordsPerUpload}
	results, err := convert.ReadAll(bytes.NewReader(raw), opt)
	if errors.Is(err, convert.ErrTooManyRecords) {
		return fmt.Errorf("this file has more than %d records, which exceeds what the dashboard will process interactively - use the ipm2csv/ipmvalidate CLI tools for very large files", maxRecordsPerUpload)
	}
	if len(results) == 0 {
		if err != nil {
			return fmt.Errorf("error reading the file: %w", err)
		}
		return fmt.Errorf("no records were found in this file — check the encoding/blocked options")
	}
	if err != nil {
		// Records were read before the failure. Discarding them would
		// throw away the evidence of where the file breaks, which is
		// exactly what an operator opened this file to find - so keep
		// them and make the truncation explicit in the UI.
		sess.ScanAborted = fmt.Sprintf("The file could not be read to its end: %v. Everything below describes only the %d record(s) read before that point.", err, len(results))
	}

	sess.Encoding = enc
	sess.Blocked1014 = blocked
	sess.Results = results
	sess.Role = role
	sess.Summary = validate.Summarize(results, validate.Options{Role: role})
	rebuildRows(sess)
	return nil
}

func (s *Server) populateVisaSession(sess *Session, raw []byte, r *http.Request) error {
	format := baseii.FormatCTF
	if r.FormValue("visa_format") == "itf" {
		format = baseii.FormatITF
	}

	framing, err := resolveVisaFraming(raw, r.FormValue("framing"), format)
	if err != nil {
		return err
	}

	enc, err := resolveVisaEncoding(raw, r.FormValue("encoding"), format, framing)
	if err != nil {
		return err
	}

	reader := baseii.NewReader(bytes.NewReader(raw), format, framing)
	// The cap is enforced while grouping, not after: checking the length
	// of the finished slice means the memory has already been spent.
	txns, err := baseii.GroupTransactions(reader, enc, maxRecordsPerUpload)
	if errors.Is(err, baseii.ErrTooManyTransactions) {
		return fmt.Errorf("this file has more than %d transactions, which exceeds what the dashboard will process interactively - use the visa2csv/visavalidate CLI tools for very large files", maxRecordsPerUpload)
	}
	if len(txns) == 0 {
		if err != nil {
			return fmt.Errorf("error reading the file: %w", err)
		}
		return fmt.Errorf("no transactions were found in this file — check the encoding/format options")
	}
	if err != nil {
		sess.ScanAborted = fmt.Sprintf("The file could not be read to its end: %v. Everything below describes only the %d transaction(s) read before that point.", err, len(txns))
	}

	sess.VisaFormat = format
	sess.VisaEncoding = enc
	sess.VisaTransactions = txns
	sess.VisaSummary = baseii.Summarize(txns)
	rebuildRows(sess)
	return nil
}

// rebuildRows recomputes the records table's view model. Called once per
// parse and again whenever the validation role changes, so that paging
// through the table is a slice of a prepared list rather than a fresh
// walk of every record in the file.
func rebuildRows(sess *Session) {
	if sess.Scheme == SchemeVisa {
		findingsByTxn := visaFindingsByNumber(sess.VisaSummary.Findings)
		rows := make([]RecordRow, 0, len(sess.VisaTransactions))
		for _, t := range sess.VisaTransactions {
			rows = append(rows, recordRowFromVisa(t, findingsByTxn))
		}
		sess.Rows = rows
		return
	}
	findingsByRecord := findingsByRecordNumber(sess.Summary.Findings)
	rows := make([]RecordRow, 0, len(sess.Results))
	for _, rr := range sess.Results {
		rows = append(rows, recordRowFromMastercard(rr, findingsByRecord))
	}
	sess.Rows = rows
}

func (s *Server) renderUploadError(w http.ResponseWriter, msg string) {
	w.WriteHeader(http.StatusOK)
	_ = s.tmpl.ExecuteTemplate(w, "upload_error.html", map[string]any{"Error": msg})
}

func resolveMastercardEncoding(raw []byte, requested string, blocked bool) (iso8583.Encoding, error) {
	switch requested {
	case "ascii":
		return iso8583.EncodingASCII, nil
	case "ebcdic":
		return iso8583.EncodingEBCDIC, nil
	case "auto", "":
	default:
		return "", fmt.Errorf("unknown encoding %q", requested)
	}
	results, err := convert.ReadAll(bytes.NewReader(raw), convert.Options{
		Encoding: iso8583.EncodingASCII, Blocked1014: blocked, IncludeAll: true, MaxRecords: 1, KeepRaw: true,
	})
	if err != nil && !errors.Is(err, convert.ErrTooManyRecords) || len(results) == 0 || len(results[0].Raw) < 4 {
		return iso8583.EncodingASCII, nil
	}
	return iso8583.DetectEncoding(results[0].Raw[:4]), nil
}

func resolveVisaFraming(raw []byte, requested string, format baseii.Format) (baseii.Framing, error) {
	switch requested {
	case "fixed":
		return baseii.FramingFixed, nil
	case "lines":
		return baseii.FramingLines, nil
	case "auto", "":
	default:
		return "", fmt.Errorf("unknown framing %q", requested)
	}
	n := format.RecordLength() + 8
	if n > len(raw) {
		n = len(raw)
	}
	return baseii.DetectFraming(raw[:n], format), nil
}

func resolveVisaEncoding(raw []byte, requested string, format baseii.Format, framing baseii.Framing) (baseii.Encoding, error) {
	switch requested {
	case "ascii":
		return baseii.EncodingASCII, nil
	case "ebcdic":
		return baseii.EncodingEBCDIC, nil
	case "auto", "":
	default:
		return "", fmt.Errorf("unknown encoding %q", requested)
	}
	reader := baseii.NewReader(bytes.NewReader(raw), format, framing)
	rec, err := reader.Next()
	if err != nil || len(rec) < 2 {
		return baseii.EncodingASCII, nil
	}
	return baseii.DetectEncoding(rec), nil
}

// ------------------------------------------------------------ overview ----

func currentGenericSummary(sess *Session) GenericSummary {
	if sess.Scheme == SchemeVisa {
		return summaryFromVisa(sess.VisaSummary)
	}
	return summaryFromMastercard(sess.Summary)
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sessionFromRequest(r)
	if !ok {
		s.sessionExpired(w)
		return
	}
	sess.Lock()
	data := map[string]any{"Session": sess, "Scheme": sess.Scheme, "Summary": currentGenericSummary(sess), "Role": sess.Role, "ScanAborted": sess.ScanAborted,
		"Strip": buildFileStrip(sess.Rows, maxStripSegments)}
	sess.Unlock()
	s.renderPartial(w, "overview.html", data)
}

func (s *Server) handleRevalidate(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sessionFromRequest(r)
	if !ok {
		s.sessionExpired(w)
		return
	}
	sess.Lock()
	if sess.Scheme != SchemeVisa {
		role := validate.RoleOrg
		if r.FormValue("role") == "dst" {
			role = validate.RoleDst
		}
		sess.Role = role
		sess.Summary = validate.Summarize(sess.Results, validate.Options{Role: role})
		rebuildRows(sess)
	}
	data := map[string]any{"Session": sess, "Scheme": sess.Scheme, "Summary": currentGenericSummary(sess), "Role": sess.Role, "ScanAborted": sess.ScanAborted,
		"Strip": buildFileStrip(sess.Rows, maxStripSegments)}
	sess.Unlock()
	s.renderPartial(w, "overview.html", data)
}

// -------------------------------------------------------------- records ----

func (s *Server) handleRecords(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sessionFromRequest(r)
	if !ok {
		s.sessionExpired(w)
		return
	}
	page := parseIntDefault(r.URL.Query().Get("page"), 1)
	filter := r.URL.Query().Get("filter") // "", "errors", "warnings"
	selected := parseIntDefault(r.URL.Query().Get("selected"), 0)

	sess.Lock()
	scheme := sess.Scheme
	var rows []RecordRow
	var counts FilterCounts
	var detail *RecordDetail

	// Rows were built once when the file was parsed; paging and filtering
	// are a walk of that prepared list rather than a fresh decode of every
	// record in the file on every click.
	for _, row := range sess.Rows {
		counts.add(row)
		if matchesFilter(filter, row) {
			rows = append(rows, row)
		}
	}

	if selected > 0 {
		if scheme == SchemeVisa {
			if t, found := findVisaTransaction(sess.VisaTransactions, selected); found {
				d := recordDetailFromVisa(t, visaFindingsByNumber(sess.VisaSummary.Findings)[selected])
				detail = &d
			}
		} else {
			if rr, found := findRecord(sess.Results, selected); found {
				d := recordDetailFromMastercard(rr, findingsByRecordNumber(sess.Summary.Findings)[selected])
				detail = &d
			}
		}
	}
	sess.Unlock()

	pag := newPagination(page, defaultPageSize, len(rows))
	pageRows := rows[pag.StartIdx():pag.EndIdx()]

	data := map[string]any{
		"Rows":       pageRows,
		"Pagination": pag,
		"Filter":     filter,
		"Selected":   selected,
		"Scheme":     scheme,
		"Counts":     counts,
	}
	if detail != nil {
		data["Detail"] = *detail
	}
	s.renderPartial(w, "records.html", data)
}

// matchesFilter applies the records table's severity filter. The two
// severities are counted separately on the row, so "warnings" means
// "has at least one WARNING" rather than "is not clean" - otherwise a
// record whose only finding is an error shows up under both chips.
func matchesFilter(filter string, row RecordRow) bool {
	switch filter {
	case "errors":
		return row.Errors > 0
	case "warnings":
		return row.Warnings > 0
	default:
		return true
	}
}

func (s *Server) handleRecordDetail(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sessionFromRequest(r)
	if !ok {
		s.sessionExpired(w)
		return
	}
	recNum := parseIntDefault(r.URL.Query().Get("record"), 0)

	sess.Lock()
	var detail RecordDetail
	found := false
	if sess.Scheme == SchemeVisa {
		findingsByTxn := visaFindingsByNumber(sess.VisaSummary.Findings)
		if t, ok := findVisaTransaction(sess.VisaTransactions, recNum); ok {
			detail = recordDetailFromVisa(t, findingsByTxn[recNum])
			found = true
		}
	} else {
		findingsByRecord := findingsByRecordNumber(sess.Summary.Findings)
		if rr, ok := findRecord(sess.Results, recNum); ok {
			detail = recordDetailFromMastercard(rr, findingsByRecord[recNum])
			found = true
		}
	}
	sess.Unlock()

	if !found {
		s.renderPartial(w, "record_detail.html", RecordDetail{NotFound: true})
		return
	}
	s.renderPartial(w, "record_detail.html", detail)
}

// -------------------------------------------------------------- findings ----

func (s *Server) handleFindings(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sessionFromRequest(r)
	if !ok {
		s.sessionExpired(w)
		return
	}
	page := parseIntDefault(r.URL.Query().Get("page"), 1)
	severity := r.URL.Query().Get("severity") // "", "ERROR", "WARNING"

	sess.Lock()
	all := currentGenericSummary(sess).Findings
	sess.Unlock()

	var filtered []GenericFinding
	var counts FilterCounts
	for _, f := range all {
		counts.Total++
		if f.Severity == "ERROR" {
			counts.Errors++
		} else {
			counts.Warnings++
		}
		if severity != "" && f.Severity != severity {
			continue
		}
		filtered = append(filtered, f)
	}

	pag := newPagination(page, defaultFindingsSize, len(filtered))
	pageFindings := filtered[pag.StartIdx():pag.EndIdx()]

	s.renderPartial(w, "findings.html", map[string]any{
		"Findings":   pageFindings,
		"Pagination": pag,
		"Severity":   severity,
		"Total":      len(filtered),
		"Counts":     counts,
	})
}

// --------------------------------------------------------------- export ----

func (s *Server) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sessionFromRequest(r)
	if !ok {
		http.Error(w, "session expired — please re-upload the file", http.StatusBadRequest)
		return
	}
	sess.Lock()
	scheme := sess.Scheme
	filename := sess.Filename
	includeAll := sess.IncludeAll
	sess.Unlock()

	switch r.URL.Query().Get("include_all") {
	case "on":
		includeAll = true
	case "off":
		includeAll = false
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.csv"`, baseName(filename)))

	if scheme == SchemeVisa {
		sess.Lock()
		txns := sess.VisaTransactions
		sess.Unlock()
		if err := baseii.WriteCSV(w, txns, includeAll); err != nil {
			log.Printf("export csv error: %v", err)
		}
		return
	}

	sess.Lock()
	results := sess.Results
	opt := convert.Options{Encoding: sess.Encoding, Blocked1014: sess.Blocked1014, IncludeAll: includeAll}
	sess.Unlock()
	if err := convert.WriteCSV(w, results, opt); err != nil {
		log.Printf("export csv error: %v", err)
	}
}

func (s *Server) handleExportReport(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sessionFromRequest(r)
	if !ok {
		http.Error(w, "session expired — please re-upload the file", http.StatusBadRequest)
		return
	}
	sess.Lock()
	scheme := sess.Scheme
	filename := sess.Filename
	sess.Unlock()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s_validation_report.txt"`, baseName(filename)))

	if scheme == SchemeVisa {
		sess.Lock()
		summary := sess.VisaSummary
		sess.Unlock()
		baseii.WriteReport(w, summary, 0)
		return
	}

	sess.Lock()
	summary := sess.Summary
	role := sess.Role
	sess.Unlock()
	validate.WriteReport(w, summary, validate.Options{Role: role}, 0)
}

// ------------------------------------------------------------- helpers ----

func (s *Server) sessionFromRequest(r *http.Request) (*Session, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return nil, false
	}
	return s.store.Get(c.Value)
}

func (s *Server) sessionExpired(w http.ResponseWriter) {
	w.Header().Set("HX-Redirect", "/")
	w.WriteHeader(http.StatusOK)
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Rendered pages and partials echo the uploaded clearing file's own
	// contents back to the browser, so they must not be stored by the
	// browser or any intermediary - unlike /static/, which is cached hard.
	w.Header().Set("Cache-Control", "no-store")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template error (%s): %v", name, err)
		http.Error(w, "internal error rendering page", http.StatusInternalServerError)
	}
}

func (s *Server) renderPartial(w http.ResponseWriter, name string, data any) {
	s.render(w, name, data)
}

func findingsByRecordNumber(findings []validate.Finding) map[int][]validate.Finding {
	m := make(map[int][]validate.Finding)
	for _, f := range findings {
		m[f.RecordNumber] = append(m[f.RecordNumber], f)
	}
	return m
}

func visaFindingsByNumber(findings []baseii.Finding) map[int][]baseii.Finding {
	m := make(map[int][]baseii.Finding)
	for _, f := range findings {
		m[f.TransactionNumber] = append(m[f.TransactionNumber], f)
	}
	return m
}

func findRecord(results []convert.RecordResult, recNum int) (convert.RecordResult, bool) {
	idx := recNum - 1
	if idx < 0 || idx >= len(results) || results[idx].RecordNumber != recNum {
		return convert.RecordResult{}, false
	}
	return results[idx], true
}

func findVisaTransaction(txns []baseii.Transaction, num int) (baseii.Transaction, bool) {
	idx := num - 1
	if idx < 0 || idx >= len(txns) || txns[idx].TransactionNumber != num {
		return baseii.Transaction{}, false
	}
	return txns[idx], true
}

func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return def
	}
	return n
}

// baseName strips any directory path from an uploaded filename and
// sanitizes what's left for safe use inside a Content-Disposition header
// value. The filename comes directly from the multipart upload (i.e. it's
// attacker-controlled input): even though Go's net/http already rejects
// header values containing raw CR/LF, this defensively allowlists a safe
// character set rather than relying on that alone.
func baseName(filename string) string {
	for i := len(filename) - 1; i >= 0; i-- {
		if filename[i] == '/' || filename[i] == '\\' {
			filename = filename[i+1:]
			break
		}
	}
	if filename == "" {
		return "export"
	}
	var b strings.Builder
	for _, r := range filename {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_', r == ' ':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.TrimSpace(b.String())
	if len(out) > 100 {
		out = out[:100]
	}
	if out == "" {
		return "export"
	}
	return out
}
