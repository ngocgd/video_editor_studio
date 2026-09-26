package bench

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/align"
	"loomtale/api/internal/providers/llm"
	"loomtale/api/internal/providers/tts"
	"loomtale/api/internal/speechrate"
	workerv1 "loomtale/api/internal/workerpb/loomtale/worker/v1"
)

// fakeWPM is the speaking rate of the fake TTS engine.
const fakeWPM = 150.0

// fakeVoiceWorker plays the Python worker: it synthesizes a constant
// tone lasting words/fakeWPM minutes and aligns audio by finding the
// non-silent runs, uploading and fetching through the given URLs.
type fakeVoiceWorker struct {
	workerv1.UnimplementedTTSServer
	workerv1.UnimplementedAlignServer
	mu       sync.Mutex
	requests []*workerv1.SynthesizeRequest
	missing  map[string]bool // engines that answer engine_not_installed
}

func httpPut(url string, data []byte) error {
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("put: %d", resp.StatusCode)
	}
	return nil
}

func httpGet(url string) ([]byte, error) {
	resp, err := http.Get(url) //nolint:gosec,noctx // a test server URL
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get: %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func (f *fakeVoiceWorker) Synthesize(req *workerv1.SynthesizeRequest, stream grpc.ServerStreamingServer[workerv1.SynthesizeEvent]) error {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	missing := f.missing[req.Engine]
	f.mu.Unlock()
	if missing {
		return status.Error(codes.FailedPrecondition, "engine_not_installed: "+req.Engine)
	}
	if ref := req.Params["reference_url"]; ref != "" {
		if req.Params["consent"] != "granted" {
			return status.Error(codes.PermissionDenied, "voice_consent_required")
		}
		if _, err := httpGet(ref); err != nil {
			return status.Error(codes.InvalidArgument, "reference: "+err.Error())
		}
	}
	rate := 24_000
	if req.Engine == EngineVI {
		rate = 48_000
	}
	seconds := float64(len(strings.Fields(req.Text))) / fakeWPM * 60
	pcm := make([]byte, 2*int(seconds*float64(rate)))
	for i := 0; i < len(pcm); i += 2 {
		pcm[i], pcm[i+1] = 0xe8, 0x03 // 1000
	}
	if err := httpPut(req.OutputPutUrl, WAV{SampleRate: rate, PCM: pcm}.Bytes()); err != nil {
		return err
	}
	_ = stream.Send(&workerv1.SynthesizeEvent{Event: &workerv1.SynthesizeEvent_Progress{Progress: &workerv1.SynthesizeProgress{Pct: 100}}})
	return stream.Send(&workerv1.SynthesizeEvent{Event: &workerv1.SynthesizeEvent_Result{Result: &workerv1.SynthesizeResult{
		OutputKey: req.Params["output_key"], DurationS: seconds,
		Metadata: map[string]string{"rtf": "0.2", "vram_peak_mb": "3000"},
	}}})
}

func (f *fakeVoiceWorker) Align(req *workerv1.AlignRequest, stream grpc.ServerStreamingServer[workerv1.AlignEvent]) error {
	data, err := httpGet(req.AudioGetUrl)
	if err != nil {
		return err
	}
	w, err := ParseWAV(data)
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	var cues []Cue
	inSpeech := false
	for i := 0; i+1 < len(w.PCM); i += 2 {
		t := float64(i/2) / float64(w.SampleRate)
		loud := w.PCM[i] != 0 || w.PCM[i+1] != 0
		switch {
		case loud && !inSpeech:
			cues = append(cues, Cue{Start: t})
			inSpeech = true
		case !loud && inSpeech:
			cues[len(cues)-1].End = t
			inSpeech = false
		}
	}
	if inSpeech {
		cues[len(cues)-1].End = w.Seconds()
	}
	body, _ := json.Marshal(map[string]any{"granularity": "segment", "matched_ratio": 1, "segments": cues})
	if err := httpPut(req.OutputPutUrl, body); err != nil {
		return err
	}
	return stream.Send(&workerv1.AlignEvent{Event: &workerv1.AlignEvent_Result{Result: &workerv1.AlignResult{
		OutputKey: req.Params["output_key"], SegmentCount: int32(len(cues)), //nolint:gosec // a test count
		Metadata: map[string]string{"matched_ratio": "1.0"},
	}}})
}

type rateQueries struct {
	recordingQueries
	rates []dbgen.UpsertVoiceRateCalibrationParams
}

func (q *rateQueries) UpsertVoiceRateCalibration(_ context.Context, p dbgen.UpsertVoiceRateCalibrationParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.rates = append(q.rates, p)
	return nil
}

func newVoiceRunner(t *testing.T, worker *fakeVoiceWorker) (*VoiceRunner, *rateQueries, *fakeResidency) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	workerv1.RegisterTTSServer(server, worker)
	workerv1.RegisterAlignServer(server, worker)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	sink, err := NewSink("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sink.Close() })
	q := &rateQueries{}
	res := &fakeResidency{}
	return &VoiceRunner{
		TTS: tts.New(workerv1.NewTTSClient(conn)), Align: align.New(workerv1.NewAlignClient(conn)),
		Residency: res, Queries: q, Sink: sink, Rates: &speechrate.Store{Queries: q}, OutDir: t.TempDir(),
	}, q, res
}

func TestParagraphsAndSentencesFitTheSuites(t *testing.T) {
	for _, lang := range []string{"en", "vi"} {
		paragraphs := Paragraphs(lang)
		if len(paragraphs) != 30 {
			t.Fatalf("%s: %d paragraphs, want 30", lang, len(paragraphs))
		}
		for _, p := range paragraphs {
			for _, s := range Sentences(p) {
				if n := len([]rune(s)); n > maxCueRunes {
					t.Errorf("%s sentence of %d runes would split into several cues: %q", lang, n, s)
				}
			}
		}
	}
	if got := Sentences("One. Two!  Three? Four… Five"); len(got) != 5 || got[3] != "Four…" {
		t.Fatalf("sentences = %q", got)
	}
}

func TestWAVRoundTripAndConcat(t *testing.T) {
	a := WAV{SampleRate: 16000, PCM: bytes.Repeat([]byte{1, 0}, 16000)}
	b := WAV{SampleRate: 16000, PCM: bytes.Repeat([]byte{2, 0}, 8000)}
	parsed, err := ParseWAV(a.Bytes())
	if err != nil || parsed.SampleRate != 16000 || !bytes.Equal(parsed.PCM, a.PCM) {
		t.Fatalf("round trip: %v", err)
	}
	joined, starts, err := ConcatWAV([]WAV{a, b}, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(joined.Seconds()-2.0) > 1e-9 || starts[0] != 0 || math.Abs(starts[1]-1.5) > 1e-9 {
		t.Fatalf("joined %.3fs, starts %v", joined.Seconds(), starts)
	}
	if _, _, err := ConcatWAV([]WAV{a, {SampleRate: 48000}}, 0); err == nil {
		t.Fatal("mixed sample rates must be refused")
	}
	if _, err := ParseWAV([]byte("not a wav file at all")); err == nil {
		t.Fatal("garbage must not parse")
	}
}

func TestSinkServesOnlyTokenPaths(t *testing.T) {
	sink, err := NewSink("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sink.Close() }()
	if err := httpPut(sink.URL("a.wav"), []byte("data")); err != nil {
		t.Fatal(err)
	}
	if got, _ := httpGet(sink.URL("a.wav")); string(got) != "data" {
		t.Fatalf("get = %q", got)
	}
	bad := strings.Replace(sink.URL("a.wav"), sink.token, strings.Repeat("0", 32), 1)
	if _, err := httpGet(bad); err == nil {
		t.Fatal("a URL without the sink's token must not be served")
	}
}

func TestDriftNeedsOneCuePerSentence(t *testing.T) {
	maxD, meanD, err := Drift([]Cue{{Start: 0.1, End: 2}, {Start: 3, End: 4.5}}, []float64{0, 3}, []float64{2, 1})
	if err != nil || math.Abs(maxD-0.5) > 1e-9 || math.Abs(meanD-0.3) > 1e-9 {
		t.Fatalf("drift = %v %v %v", maxD, meanD, err)
	}
	if _, _, err := Drift([]Cue{{}}, []float64{0, 1}, []float64{1, 1}); err == nil {
		t.Fatal("a cue count mismatch must be an error")
	}
}

func TestRunTTSRecordsEveryCaseAndCalibratesEachVoice(t *testing.T) {
	worker := &fakeVoiceWorker{}
	r, q, res := newVoiceRunner(t, worker)
	report, err := r.RunTTS(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(q.rows) != 61 {
		t.Fatalf("rows = %d, want the reference plus 30 EN and 30 VI", len(q.rows))
	}
	for _, row := range q.rows {
		if !row.Ok {
			t.Fatalf("case %s failed: %v", row.CaseName, row.Error.String)
		}
	}
	for _, req := range worker.requests {
		if req.Engine == EngineEN && (req.Params["reference_url"] == "" || req.Params["consent"] != "granted") {
			t.Fatalf("EN cases must clone the reference with consent: %v", req.Params)
		}
	}
	if len(report.Calibrations) != 2 || len(q.rates) != 2 {
		t.Fatalf("calibrations %d, stored %d", len(report.Calibrations), len(q.rates))
	}
	for _, c := range report.Calibrations {
		if math.Abs(c.WPM-fakeWPM) > 0.5 || !c.HoldoutChecked || c.HoldoutDeviation > 0.01 {
			t.Fatalf("calibration %+v", c)
		}
	}
	if !strings.HasPrefix(q.rates[0].VoiceKey, EngineEN+":ref:") || q.rates[1].VoiceKey != EngineVI+":voice:"+VoiceVI {
		t.Fatalf("voice keys %q %q", q.rates[0].VoiceKey, q.rates[1].VoiceKey)
	}
	if res.ensures != 3 { // vieneu (reference), chatterbox, vieneu
		t.Fatalf("ensures = %d", res.ensures)
	}
	for _, b := range report.Budgets {
		if !b.Known || !b.OK() {
			t.Fatalf("budget %s", b)
		}
	}
}

func TestRunTTSReportsANotInstalledEngineOnEveryCase(t *testing.T) {
	worker := &fakeVoiceWorker{missing: map[string]bool{EngineEN: true, EngineVI: true}}
	r, q, _ := newVoiceRunner(t, worker)
	report, err := r.RunTTS(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(q.rows) != 61 || len(report.Calibrations) != 0 {
		t.Fatalf("rows %d calibrations %d", len(q.rows), len(report.Calibrations))
	}
	for _, res := range report.Results {
		if res.Err == nil {
			t.Fatalf("%s should have failed", res.Name)
		}
	}
	if !strings.Contains(report.Results[1].Err.Error(), "no reference clip") {
		t.Fatalf("EN cases need the reference: %v", report.Results[1].Err)
	}
	if !strings.Contains(report.Results[40].Err.Error(), pipeline.ErrEngineNotInstalled.Error()) {
		t.Fatalf("VI cases must report engine_not_installed: %v", report.Results[40].Err)
	}
}

func TestRunAlignMeasuresDriftOnFiveMinutesPerLanguage(t *testing.T) {
	r, q, _ := newVoiceRunner(t, &fakeVoiceWorker{})
	results, budgets, err := r.RunAlign(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, res := range results {
		if res.Err != nil {
			t.Fatalf("%s: %v", res.Name, res.Err)
		}
		if res.AudioSeconds < alignTargetSeconds || res.MaxDriftS > 0.001 {
			t.Fatalf("%s: %.1fs audio, drift %.4fs", res.Name, res.AudioSeconds, res.MaxDriftS)
		}
	}
	if budgets[0].Known != true {
		t.Fatal("alignment speed must be measured")
	}
	cases := 0
	for _, row := range q.rows {
		if row.Suite == "align" && strings.HasPrefix(row.CaseName, "align-") {
			cases++
		}
	}
	if cases != 2 { // align-en and align-vi (the reference clip has its own row)
		t.Fatalf("align rows = %d", cases)
	}
	if _, err := os.Stat(filepath.Join(r.OutDir, "align-en.json")); err != nil {
		t.Fatal("the cue file must be saved for inspection")
	}
}

func TestVoiceSmokeSwitchesToOllamaAndBack(t *testing.T) {
	r, q, res := newVoiceRunner(t, &fakeVoiceWorker{})
	report, err := r.RunVoiceSmoke(context.Background(), "qwen3.5-9b")
	if err != nil {
		t.Fatal(err)
	}
	if report.Align.Err != nil || report.Align.Cues != 1 {
		t.Fatalf("align %+v", report.Align)
	}
	names := []string{}
	for _, row := range q.rows {
		names = append(names, row.CaseName)
	}
	want := "reference,en-line,switch-to-ollama,vi-after-ollama,align-en-line"
	if strings.Join(names, ",") != want {
		t.Fatalf("cases = %v", names)
	}
	if res.current == nil || res.current.Model != EngineAlign {
		t.Fatalf("resident at the end = %v", res.current)
	}
}

type fakeLLM struct{ name string }

func (f fakeLLM) Name() string { return f.name }
func (f fakeLLM) Generate(ctx context.Context, req llm.Request) (llm.Response, error) {
	return f.Stream(ctx, req, nil)
}
func (f fakeLLM) Stream(_ context.Context, req llm.Request, onDelta func(llm.Delta)) (llm.Response, error) {
	if onDelta != nil {
		onDelta(llm.Delta{Text: "1."})
	}
	return llm.Response{Text: "1. " + req.Messages[0].Text[:10], Usage: llm.Usage{Out: 40}}, nil
}

func TestLLMRunnerRecordsTimingsAndWritesARatingsSheet(t *testing.T) {
	q := &recordingQueries{}
	res := &fakeResidency{}
	r := &LLMRunner{Residency: res, Queries: q, OutDir: t.TempDir()}
	ref := &pipeline.ModelRef{Backend: "ollama", Model: "qwen3.5-9b"}
	results, budgets, err := r.Run(context.Background(), []LLMTarget{
		{Name: "qwen3.5-9b", Provider: fakeLLM{"ollama"}, Residency: ref},
		{Name: "claude-cli", Provider: fakeLLM{"claude-cli"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2*len(LLMCases) || len(q.rows) != len(results) {
		t.Fatalf("results %d rows %d", len(results), len(q.rows))
	}
	if results[0].Streamed || results[0].TokensPerSecond <= 0 {
		t.Fatalf("a one-chunk answer must report throughput over the whole request: %+v", results[0])
	}
	if !results[0].Switched || results[1].Switched || res.ensures != 1 {
		t.Fatal("only the first Ollama case carries the residency switch")
	}
	f, err := os.Open(filepath.Join(r.OutDir, "llm", "ratings.csv"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil || len(rows) != 1+len(results) || rows[0][3] != "rating_1_to_5" {
		t.Fatalf("ratings sheet %v %v", rows, err)
	}
	if len(budgets) != 3 { // two first-token budgets and the Ollama switch
		t.Fatalf("budgets %v", budgets)
	}
}
