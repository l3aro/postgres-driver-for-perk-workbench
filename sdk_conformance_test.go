package server_test

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/l3aro/perk-workbench-plugin-sdk-go/driver"
	"github.com/l3aro/perk-workbench-plugin-sdk-go/server"
)

//go:embed testdata/fixtures/*.json
var fixtureFS embed.FS

type fakeService struct {
	entered chan struct{}
	once    sync.Once
	execErr error
}

func (f *fakeService) Execute(ctx context.Context, req driver.StatementRequest) (driver.Result, error) {
	if f.execErr != nil {
		return driver.Result{}, f.execErr
	}
	if req.Statement == "BLOCK" {
		f.once.Do(func() { close(f.entered) })
		<-ctx.Done()
		return driver.Result{}, ctx.Err()
	}
	value := "ok"
	return driver.Result{Columns: []string{"value"}, ColumnTypes: []string{"TEXT"}, Rows: [][]*string{{&value}}, UntruncatedRows: [][]*string{{&value}}}, nil
}
func (f *fakeService) ExecuteReadOnly(context.Context, driver.StatementRequest) (driver.Result, error) {
	return driver.Result{}, nil
}
func (f *fakeService) Validate(context.Context, driver.StatementRequest) error { return nil }
func (f *fakeService) ListSchema(context.Context, driver.EmptyRequest) ([]driver.SchemaObject, error) {
	return []driver.SchemaObject{}, nil
}
func (f *fakeService) TableInfo(context.Context, driver.TableRequest) ([]driver.ColumnInfo, error) {
	return []driver.ColumnInfo{}, nil
}
func (f *fakeService) ListIndexes(context.Context, driver.TableRequest) ([]driver.IndexInfo, error) {
	return []driver.IndexInfo{}, nil
}
func (f *fakeService) CreateIndex(context.Context, driver.IndexChangeRequest) error   { return nil }
func (f *fakeService) ReplaceIndex(context.Context, driver.ReplaceIndexRequest) error { return nil }
func (f *fakeService) DropIndex(context.Context, driver.DropRequest) error            { return nil }
func (f *fakeService) ListForeignKeys(context.Context, driver.TableRequest) ([]driver.ForeignKeyInfo, error) {
	return []driver.ForeignKeyInfo{}, nil
}
func (f *fakeService) ListReferencingForeignKeys(context.Context, driver.TableRequest) ([]driver.ReferencingForeignKeyInfo, error) {
	return []driver.ReferencingForeignKeyInfo{}, nil
}
func (f *fakeService) ListForeignKeysAll(context.Context, driver.EmptyRequest) (map[string][]driver.ForeignKeyInfo, error) {
	return map[string][]driver.ForeignKeyInfo{}, nil
}
func (f *fakeService) ListIndexesAll(context.Context, driver.EmptyRequest) (map[string][]driver.IndexInfo, error) {
	return map[string][]driver.IndexInfo{}, nil
}
func (f *fakeService) CreateForeignKey(context.Context, driver.ForeignKeyChangeRequest) error {
	return nil
}
func (f *fakeService) ReplaceForeignKey(context.Context, driver.ReplaceForeignKeyRequest) error {
	return nil
}
func (f *fakeService) DropForeignKey(context.Context, driver.DropRequest) error      { return nil }
func (f *fakeService) AlterColumn(context.Context, driver.ColumnChangeRequest) error { return nil }
func (f *fakeService) DropColumn(context.Context, driver.DropRequest) error          { return nil }
func (f *fakeService) AddColumn(context.Context, driver.AddColumnRequest) error      { return nil }
func (f *fakeService) BrowseTable(context.Context, driver.BrowseTableRequest) (driver.Result, error) {
	return driver.Result{}, nil
}
func (f *fakeService) Close() error { return nil }

type fakeFactory struct{ service *fakeService }

func (f *fakeFactory) Capabilities() driver.Capabilities {
	return driver.Capabilities{Name: "fake", Display: "Fake", WriteCapabilities: driver.WriteCapabilities{}}
}
func (f *fakeFactory) BuildTarget(context.Context, driver.FormValues) (driver.BuildTargetResult, error) {
	return driver.BuildTargetResult{Target: "fake", OK: true}, nil
}
func (f *fakeFactory) Open(context.Context, string) (driver.OpenResult, error) {
	return driver.OpenResult{Info: driver.DatabaseInfo{Product: "fake", Version: "1"}, Service: f.service}, nil
}

type frameWriter struct {
	mu     sync.Mutex
	data   bytes.Buffer
	frames chan []byte
}

func newFrameWriter() *frameWriter { return &frameWriter{frames: make(chan []byte, 16)} }
func (w *frameWriter) Write(data []byte) (int, error) {
	copyOfData := append([]byte(nil), data...)
	w.mu.Lock()
	_, _ = w.data.Write(copyOfData)
	w.mu.Unlock()
	w.frames <- copyOfData
	return len(data), nil
}
func (w *frameWriter) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return bytes.Count(w.data.Bytes(), []byte{'\n'})
}

func writeFrame(t *testing.T, writer *io.PipeWriter, value string) {
	t.Helper()
	if _, err := io.WriteString(writer, value+"\n"); err != nil {
		t.Fatal(err)
	}
}
func readFrame(t *testing.T, frames <-chan []byte) map[string]any {
	t.Helper()
	select {
	case data := <-frames:
		var value map[string]any
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		return value
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for response")
		return nil
	}
}

func TestRunLifecycleAndCancellation(t *testing.T) {
	inputReader, inputWriter := io.Pipe()
	output := newFrameWriter()
	service := &fakeService{entered: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- server.Run(inputReader, output, &fakeFactory{service: service}) }()
	writeFrame(t, inputWriter, `{"jsonrpc":"2.0","id":1,"method":"perk/v1/initialize","params":{"protocol_version":1,"workbench_version":"test"}}`)
	initialize := readFrame(t, output.frames)
	if initialize["result"] == nil {
		t.Fatalf("initialize response = %#v", initialize)
	}
	writeFrame(t, inputWriter, `{"jsonrpc":"2.0","id":2,"method":"perk/v1/open","params":{"target":"fake"}}`)
	open := readFrame(t, output.frames)
	if open["result"].(map[string]any)["session_id"] != float64(1) {
		t.Fatalf("open response = %#v", open)
	}
	writeFrame(t, inputWriter, `{"jsonrpc":"2.0","id":3,"method":"perk/v1/execute","params":{"session_id":1,"statement":"BLOCK"}}`)
	select {
	case <-service.entered:
	case <-time.After(time.Second):
		t.Fatal("blocking handler did not start")
	}
	writeFrame(t, inputWriter, `{"jsonrpc":"2.0","method":"perk/v1/cancel","params":{"id":3}}`)
	canceled := readFrame(t, output.frames)
	if canceled["error"].(map[string]any)["code"] != float64(server.Cancelled) {
		t.Fatalf("cancellation response = %#v", canceled)
	}
	if err := inputWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Run() = %v, want nil", err)
	}
	if got := output.count(); got != 3 {
		t.Fatalf("frames after cancellation = %d, want exactly 3", got)
	}
}

func TestRunTerminalFraming(t *testing.T) {
	tests := []struct {
		name  string
		frame []byte
	}{
		{name: "malformed JSON", frame: []byte("{\n")},
		{name: "invalid UTF-8", frame: []byte{0xff, '\n'}},
		{name: "oversize", frame: append(bytes.Repeat([]byte{'x'}, driver.MaxFrameBytes), '\n')},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := newFrameWriter()
			err := server.Run(bytes.NewReader(test.frame), output, &fakeFactory{service: &fakeService{entered: make(chan struct{})}})
			if err == nil {
				t.Fatal("Run() = nil, want terminal error")
			}
			if output.count() != 0 {
				t.Fatalf("terminal output frames = %d, want 0", output.count())
			}
		})
	}
}

func TestRunStructuredAdvisoryError(t *testing.T) {
	service := &fakeService{entered: make(chan struct{}), execErr: driver.NewOperationError(driver.KindConnection, "not connected").WithAdvisory("retry later", "SELECT 1")}
	inputReader, inputWriter := io.Pipe()
	output := newFrameWriter()
	done := make(chan error, 1)
	go func() { done <- server.Run(inputReader, output, &fakeFactory{service: service}) }()
	writeFrame(t, inputWriter, `{"jsonrpc":"2.0","id":1,"method":"perk/v1/initialize","params":{"protocol_version":1,"workbench_version":"test"}}`)
	_ = readFrame(t, output.frames)
	writeFrame(t, inputWriter, `{"jsonrpc":"2.0","id":2,"method":"perk/v1/open","params":{"target":"fake"}}`)
	_ = readFrame(t, output.frames)
	writeFrame(t, inputWriter, `{"jsonrpc":"2.0","id":3,"method":"perk/v1/execute","params":{"session_id":1,"statement":"GET"}}`)
	response := readFrame(t, output.frames)
	errObject := response["error"].(map[string]any)
	data := errObject["data"].(map[string]any)
	for key, want := range map[string]string{"kind": "connection", "plugin": "fake", "method": "perk/v1/execute", "hint": "retry later", "suggested_statement": "SELECT 1"} {
		if data[key] != want {
			t.Fatalf("error.data[%q] = %v, want %q", key, data[key], want)
		}
	}
	_ = inputWriter.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestRunValidationErrorKind(t *testing.T) {
	inputReader, inputWriter := io.Pipe()
	output := newFrameWriter()
	service := &fakeService{
		entered: make(chan struct{}),
		execErr: driver.NewOperationError(driver.KindValidation, "statement is empty"),
	}
	done := make(chan error, 1)
	go func() { done <- server.Run(inputReader, output, &fakeFactory{service: service}) }()

	writeFrame(t, inputWriter, `{"jsonrpc":"2.0","id":1,"method":"perk/v1/initialize","params":{"protocol_version":1,"workbench_version":"test"}}`)
	_ = readFrame(t, output.frames)
	writeFrame(t, inputWriter, `{"jsonrpc":"2.0","id":2,"method":"perk/v1/open","params":{"target":"fake"}}`)
	_ = readFrame(t, output.frames)
	writeFrame(t, inputWriter, `{"jsonrpc":"2.0","id":3,"method":"perk/v1/execute","params":{"session_id":1,"statement":""}}`)
	response := readFrame(t, output.frames)
	errObject := response["error"].(map[string]any)
	data := errObject["data"].(map[string]any)
	if data["kind"] != string(driver.KindValidation) {
		t.Fatalf("error.data.kind = %v, want %q", data["kind"], driver.KindValidation)
	}

	_ = inputWriter.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestProtocolFixturesManifest(t *testing.T) {
	manifestBytes, err := fixtureFS.ReadFile("testdata/fixtures/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Fixtures []struct {
			File  string `json:"file"`
			Valid bool   `json:"valid"`
			Code  int    `json:"code"`
		} `json:"fixtures"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Fixtures) < 20 {
		t.Fatalf("fixture manifest entries = %d, want canonical set", len(manifest.Fixtures))
	}
	valid, invalid := 0, 0
	for _, entry := range manifest.Fixtures {
		data, err := fixtureFS.ReadFile("testdata/fixtures/" + entry.File)
		if err != nil {
			t.Fatalf("fixture %s: %v", entry.File, err)
		}
		data = bytes.TrimSpace(data)
		if !json.Valid(data) {
			t.Fatalf("fixture %s is not JSON", entry.File)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil || object == nil {
			t.Fatalf("fixture %s is not an object", entry.File)
		}
		data, err = json.Marshal(object)
		if err != nil {
			t.Fatalf("fixture %s compact frame: %v", entry.File, err)
		}
		if entry.Valid {
			valid++
		} else {
			invalid++
		}
		var method string
		if rawMethod, ok := object["method"]; ok {
			_ = json.Unmarshal(rawMethod, &method)
		}
		input := append([]byte(nil), data...)
		if method != "perk/v1/initialize" && method != "perk/v1/cancel" {
			initial := []byte(`{"jsonrpc":"2.0","id":100,"method":"perk/v1/initialize","params":{"protocol_version":1,"workbench_version":"fixture"}}`)
			initial = append(initial, '\n')
			input = append(initial, input...)
		}
		input = append(input, '\n')
		output := newFrameWriter()
		err = server.Run(bytes.NewReader(input), output, &fakeFactory{service: &fakeService{entered: make(chan struct{})}})
		if err != nil {
			t.Fatalf("fixture %s Run() = %v", entry.File, err)
		}
		if !entry.Valid && output.count() == 0 {
			t.Fatalf("invalid fixture %s produced no rejection response", entry.File)
		}
		if entry.Valid && method != "perk/v1/cancel" && output.count() == 0 {
			t.Fatalf("valid fixture %s produced no response", entry.File)
		}
		if entry.Code != 0 && method == "" {
			var fixtureError struct {
				Code int `json:"code"`
			}
			if rawError, ok := object["error"]; !ok || json.Unmarshal(rawError, &fixtureError) != nil || fixtureError.Code != entry.Code {
				t.Fatalf("fixture %s error code does not match manifest code %d", entry.File, entry.Code)
			}
		}
		if entry.Code != 0 && method != "" {
			output.mu.Lock()
			lines := bytes.Split(bytes.TrimSpace(output.data.Bytes()), []byte{'\n'})
			output.mu.Unlock()
			if len(lines) == 0 || len(lines[len(lines)-1]) == 0 {
				t.Fatalf("fixture %s produced no response", entry.File)
			}
			var response map[string]any
			if err := json.Unmarshal(lines[len(lines)-1], &response); err != nil {
				t.Fatalf("fixture %s response: %v", entry.File, err)
			}
			errorValue, ok := response["error"].(map[string]any)
			if !ok || errorValue["code"] != float64(entry.Code) {
				t.Fatalf("fixture %s error = %#v, want code %d", entry.File, response, entry.Code)
			}
		}
	}
	if valid == 0 || invalid == 0 {
		t.Fatalf("manifest valid=%d invalid=%d, want both", valid, invalid)
	}
}

func TestDriverBounds(t *testing.T) {
	tooMany := make([]driver.QueryCommand, driver.MaxQueryCommands+1)
	for i := range tooMany {
		tooMany[i] = driver.QueryCommand{Name: "C", Usage: "GET key", Summary: "Get"}
	}
	if err := driver.ValidateCapabilities(driver.Capabilities{Name: "fake", Display: "Fake", WriteCapabilities: driver.WriteCapabilities{}, QueryLanguage: &driver.QueryLanguage{Name: "x", EditorLabel: "x", Placeholder: "x", Commands: tooMany}}); err == nil {
		t.Fatal("command bound accepted")
	}
	tooManyViews := make([]driver.CustomWorkspaceView, driver.MaxCustomWorkspaceViews+1)
	for i := range tooManyViews {
		tooManyViews[i] = driver.CustomWorkspaceView{ID: string(rune('a' + i)), Label: string(rune('A' + i)), Scopes: []driver.WorkspaceViewScope{driver.WorkspaceViewDatabase}}
	}
	if err := driver.ValidateCapabilities(driver.Capabilities{Name: "fake", Display: "Fake", WriteCapabilities: driver.WriteCapabilities{}, Workspace: &driver.WorkspaceCapability{CustomViews: tooManyViews}}); err == nil {
		t.Fatal("workspace view bound accepted")
	}
}

var _ = bufio.NewReader
