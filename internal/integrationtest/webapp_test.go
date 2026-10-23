package integrationtest

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mciipm/internal/vbs"
	"mciipm/internal/webapp"
)

// uploadFile posts a VBS-framed Mastercard file to the dashboard and
// returns the server plus the session cookie the upload set.
func uploadFile(t *testing.T, records ...[]byte) (*webapp.Server, *http.Cookie) {
	t.Helper()

	var file bytes.Buffer
	w := vbs.NewWriter(&file)
	for _, rec := range records {
		if err := w.Write(rec); err != nil {
			t.Fatalf("vbs write: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("vbs close: %v", err)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for field, value := range map[string]string{
		"scheme":   "mastercard",
		"encoding": "ascii",
		"role":     "org",
	} {
		if err := mw.WriteField(field, value); err != nil {
			t.Fatalf("write field %s: %v", field, err)
		}
	}
	part, err := mw.CreateFormFile("file", "test.ipm")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(file.Bytes()); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	srv, err := webapp.NewServer()
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("upload status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if redirect := rec.Header().Get("HX-Redirect"); redirect != "/dashboard" {
		t.Fatalf("upload did not redirect to the dashboard (body: %s)", rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("upload set no session cookie")
	}
	return srv, cookies[0]
}

func get(t *testing.T, srv *webapp.Server, cookie *http.Cookie, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", path, rec.Code)
	}
	return rec
}

// errorOnlyRecord is missing an Org-mandatory field: one ERROR, no
// warnings. warningOnlyRecord has a PAN that fails Luhn: one WARNING, no
// errors. Distinct DE 71 values keep the file-level message-number rule
// out of the way.
func errorOnlyRecord(messageNumber string) []byte {
	f := compliantFields()
	delete(f, 33) // DE33 Forwarding Institution ID Code is Org-mandatory
	f[71] = buildFixed(messageNumber)
	return buildFirstPresentment(f, nil, nil)
}

func warningOnlyRecord(messageNumber string) []byte {
	f := compliantFields()
	f[2] = buildLLVAR("4000000000000001") // fails Luhn; not a substring of DE31's test value
	f[71] = buildFixed(messageNumber)
	return buildFirstPresentment(f, nil, nil)
}

func TestRecordsFilter_SeparatesErrorsFromWarnings(t *testing.T) {
	// Record 1: error only. Record 2: warning only. Record 3: clean.
	srv, cookie := uploadFile(t,
		errorOnlyRecord("00000001"),
		warningOnlyRecord("00000002"),
		func() []byte {
			f := compliantFields()
			f[71] = buildFixed("00000003")
			return buildFirstPresentment(f, nil, nil)
		}(),
	)

	all := get(t, srv, cookie, "/partial/records?page=1&filter=").Body.String()
	if got := strings.Count(all, "<tr"); got < 3 {
		t.Fatalf("unfiltered table has %d rows, want at least 3 (+header)", got)
	}

	errs := get(t, srv, cookie, "/partial/records?page=1&filter=errors").Body.String()
	warns := get(t, srv, cookie, "/partial/records?page=1&filter=warnings").Body.String()

	if errs == warns {
		t.Fatal("the errors and warnings filters returned identical tables")
	}
	if !strings.Contains(errs, "1 error") {
		t.Errorf("errors filter did not show the error record:\n%s", errs)
	}
	if strings.Contains(errs, "1 warning") {
		t.Errorf("errors filter leaked the warning-only record:\n%s", errs)
	}
	if !strings.Contains(warns, "1 warning") {
		t.Errorf("warnings filter did not show the warning record:\n%s", warns)
	}
	if strings.Contains(warns, "1 error") {
		t.Errorf("warnings filter leaked the error-only record:\n%s", warns)
	}
}

func TestRecordDetail_MasksPAN(t *testing.T) {
	srv, cookie := uploadFile(t, warningOnlyRecord("00000001"))

	detail := get(t, srv, cookie, "/partial/record?record=1").Body.String()
	if strings.Contains(detail, "4000000000000001") {
		t.Error("record detail rendered the full PAN")
	}
	if !strings.Contains(detail, "400000•") || !strings.Contains(detail, "•0001") {
		t.Errorf("record detail did not show the permitted first-6/last-4 digits:\n%s", detail)
	}
	// The Luhn warning names no PAN, but the non-numeric rule quotes the
	// value back - neither may carry a full PAN into the page.
	if strings.Count(detail, "4000000000000001") != 0 {
		t.Error("a finding message leaked the full PAN")
	}
}

func TestHTMLResponsesAreNotCacheable(t *testing.T) {
	srv, cookie := uploadFile(t, warningOnlyRecord("00000001"))

	for _, path := range []string{"/dashboard", "/partial/overview", "/partial/records?page=1", "/partial/record?record=1"} {
		rec := get(t, srv, cookie, path)
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s Cache-Control = %q, want no-store", path, got)
		}
	}
}
