package review

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// fakeClaude writes a shell script that stands in for the Claude CLI: it
// drains stdin, then runs body. It returns a provider that runs it.
func fakeClaude(t *testing.T, body string) *claudeCLIProvider {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\ncat >/dev/null\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &claudeCLIProvider{model: "opus", binaryPath: path, env: os.Environ()}
}

func TestClaudeRun_ParsesResultEvent(t *testing.T) {
	p := fakeClaude(t, `echo '{"type":"system","subtype":"init","model":"claude-opus-5-5"}'
echo '{"type":"assistant","message":{}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"no findings","total_cost_usd":0.5,"duration_ms":1200,"usage":{"input_tokens":10,"output_tokens":3},"modelUsage":{"claude-opus-5-5":{"inputTokens":10,"outputTokens":3,"costUSD":0.5}}}'`)
	res, err := p.Run(context.Background(), "prompt", RunOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "no findings" || res.Usage.Model != "claude-opus-5-5" || res.Usage.OutputTokens != 3 || res.DurationMS != 1200 {
		t.Errorf("unexpected result: %+v", res)
	}
}

// The CLI retries a 429 with backoff that outlasts the review timeout and,
// in --print mode, prints nothing while it waits. The provider must stop it
// on the first 429 retry event instead.
func TestClaudeRun_StopsOnRateLimitRetry(t *testing.T) {
	p := fakeClaude(t, `echo '{"type":"system","subtype":"init"}'
echo '{"type":"system","subtype":"api_retry","attempt":1,"max_retries":10,"retry_delay_ms":522,"error_status":429,"error":"rate_limit"}'
exec sleep 30`)
	start := time.Now()
	_, err := p.Run(context.Background(), "prompt", RunOpts{Timeout: 20 * time.Second})
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("Run took %s; want it to stop at the 429", elapsed)
	}
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Kind != ErrorKindRateLimit || pe.Status != 429 {
		t.Fatalf("err = %v, want a claude 429 rate-limit ProviderError", err)
	}
	if !strings.Contains(pe.Message, "429 rate_limit") || !strings.Contains(pe.Message, "opus") {
		t.Errorf("message should name the error and the model: %q", pe.Message)
	}
}

// Other retried errors are left to the CLI; when the call times out, the
// error says what the CLI was retrying and carries its stderr.
func TestClaudeRun_TimeoutNamesRetriedError(t *testing.T) {
	p := fakeClaude(t, `echo '{"type":"system","subtype":"api_retry","attempt":3,"max_retries":10,"retry_delay_ms":4000,"error_status":529,"error":"overloaded"}'
echo 'stream stalled' >&2
exec sleep 30`)
	_, err := p.Run(context.Background(), "prompt", RunOpts{Timeout: time.Second})
	if err == nil {
		t.Fatal("want a timeout error")
	}
	for _, want := range []string{"claude timed out after 1s", "529 overloaded, retry 3/10", "stream stalled"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %q", want, err)
		}
	}
	if !strings.HasPrefix(errorStatusDescription(err), "timeout: ") {
		t.Errorf("status description should stay a timeout: %q", errorStatusDescription(err))
	}
}

// A child the CLI started can keep stdout open after the CLI is killed.
// WaitDelay must still end the call shortly after the timeout.
func TestClaudeRun_TimeoutWithChildHoldingStdout(t *testing.T) {
	p := fakeClaude(t, `sleep 60 &
exec sleep 60`)
	start := time.Now()
	_, err := p.Run(context.Background(), "prompt", RunOpts{Timeout: time.Second})
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Fatalf("Run took %s; a child holding stdout open hung the timeout", elapsed)
	}
	if err == nil || !strings.Contains(err.Error(), "claude timed out after 1s") {
		t.Fatalf("err = %v, want a timeout", err)
	}
}

// A CLI that exits 0 but leaves a child holding stdout open still succeeds
// once WaitDelay closes the pipes.
func TestClaudeRun_ResultKeptWhenChildOutlivesCLI(t *testing.T) {
	p := fakeClaude(t, `echo '{"type":"result","is_error":false,"result":"no findings"}'
sleep 60 &
exit 0`)
	res, err := p.Run(context.Background(), "prompt", RunOpts{Timeout: 30 * time.Second})
	if err != nil || res.Text != "no findings" {
		t.Fatalf("res = %+v, err = %v; want the result kept", res, err)
	}
}

// Events split across writes, and a last line with no newline, still parse.
func TestClaudeStream_SplitWritesAndUnterminatedLine(t *testing.T) {
	s := &claudeStream{model: "opus", abort: func() {}}
	_, _ = s.Write([]byte(`{"type":"system","subtype":"api_retry","attempt":2,"max_ret`))
	_, _ = s.Write([]byte("ries\":10,\"error_status\":529,\"error\":\"overloaded\"}\n{\"type\":\"result\",\"result\":\"done\"}"))
	s.flush()
	if s.lastRetry == nil || s.lastRetry.MaxRetries != 10 || s.lastRetry.describe() != "529 overloaded" {
		t.Errorf("retry event not parsed: %+v", s.lastRetry)
	}
	if s.result == nil || s.result.Result != "done" {
		t.Errorf("unterminated result line not parsed: %+v", s.result)
	}
}

func TestClaudeRun_ErrorResultIsClassified(t *testing.T) {
	p := fakeClaude(t, `echo '{"type":"result","is_error":true,"api_error_status":529,"result":"API Error: 529 Overloaded."}'
exit 1`)
	_, err := p.Run(context.Background(), "prompt", RunOpts{})
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Kind != ErrorKindServer || pe.Message != "API Error: 529 Overloaded." {
		t.Fatalf("err = %v, want a classified 529", err)
	}
}

func TestClaudeRun_FailureWithoutResultShowsStderr(t *testing.T) {
	p := fakeClaude(t, `echo 'error: unknown option --restricted' >&2
exit 2`)
	_, err := p.Run(context.Background(), "prompt", RunOpts{})
	if err == nil || !strings.Contains(err.Error(), "unknown option --restricted") {
		t.Fatalf("err = %v, want the CLI's stderr", err)
	}
}

func TestClaudeRun_PlainTextOutput(t *testing.T) {
	p := fakeClaude(t, `echo 'plain answer'`)
	res, err := p.Run(context.Background(), "prompt", RunOpts{})
	if err != nil || strings.TrimSpace(res.Text) != "plain answer" {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
}

func TestTailBuffer_KeepsEnd(t *testing.T) {
	b := &tailBuffer{max: 5}
	_, _ = b.Write([]byte("abc"))
	_, _ = b.Write([]byte("defgh"))
	if b.String() != "defgh" {
		t.Errorf("got %q", b.String())
	}
}

// countedFakeClaude is fakeClaude with a stall watchdog of one second, and a
// counter: the script sees its invocation number as $n, and calls reports
// how many times it ran.
func countedFakeClaude(t *testing.T, body string) (p *claudeCLIProvider, calls func() int) {
	t.Helper()
	p = fakeClaude(t, `d=$(dirname "$0"); n=$(( $(cat "$d/n" 2>/dev/null || echo 0) + 1 )); echo $n > "$d/n"
`+body)
	p.stallAfter = time.Second
	return p, func() int {
		b, _ := os.ReadFile(filepath.Join(filepath.Dir(p.binaryPath), "n"))
		n := 0
		for _, c := range strings.TrimSpace(string(b)) {
			n = n*10 + int(c-'0')
		}
		return n
	}
}

const (
	evRequesting = `echo '{"type":"system","subtype":"status","status":"requesting"}'`
	evResult     = `echo '{"type":"result","is_error":false,"result":"no findings"}'`
)

// The PR this was written for: the CLI sent its request, then nothing, until
// the 10-minute timeout. A silent CLI is stopped after stallAfter and the
// call is run again.
func TestClaudeRun_StallIsRetried(t *testing.T) {
	p, calls := countedFakeClaude(t, `if [ $n -eq 1 ]; then `+evRequesting+`; exec sleep 30; fi
`+evResult)
	start := time.Now()
	res, err := p.Run(context.Background(), "prompt", RunOpts{Timeout: 20 * time.Second})
	if err != nil || res.Text != "no findings" {
		t.Fatalf("res = %+v, err = %v; want the retry's result", res, err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("Run took %s; want the stall caught after ~1s", elapsed)
	}
	if calls() != 2 {
		t.Errorf("CLI ran %d times, want 2", calls())
	}
}

func TestClaudeRun_SecondStallIsReported(t *testing.T) {
	p, calls := countedFakeClaude(t, evRequesting+`
echo 'still connecting' >&2
exec sleep 30`)
	_, err := p.Run(context.Background(), "prompt", RunOpts{Timeout: 20 * time.Second})
	if err == nil {
		t.Fatal("want a stall error")
	}
	want := "claude timed out: the CLI sent nothing for 1s (last event: system/status requesting, Ns into the call), on the retry too\n" +
		"claude stderr:\nstill connecting"
	if got := normalizeElapsed(err.Error()); got != want {
		t.Errorf("error =\n%s\nwant\n%s", got, want)
	}
	if !strings.HasPrefix(errorStatusDescription(err), "timeout: ") {
		t.Errorf("status description should be a timeout: %q", errorStatusDescription(err))
	}
	if calls() != 2 {
		t.Errorf("CLI ran %d times, want 2", calls())
	}
}

// normalizeElapsed replaces the time into the call in a stall message,
// which depends on how fast the fake CLI started.
func normalizeElapsed(s string) string {
	return regexp.MustCompile(`\d+s into the call`).ReplaceAllString(s, "Ns into the call")
}

// Events keep the call alive however long it runs.
func TestClaudeRun_SteadyEventsAreNotAStall(t *testing.T) {
	p, calls := countedFakeClaude(t, `for i in 1 2 3 4 5 6 7 8; do echo '{"type":"stream_event","event":{"type":"content_block_delta"}}'; sleep 0.3; done
`+evResult)
	res, err := p.Run(context.Background(), "prompt", RunOpts{Timeout: 20 * time.Second})
	if err != nil || res.Text != "no findings" || calls() != 1 {
		t.Fatalf("res = %+v, err = %v, calls = %d; want one uninterrupted run", res, err, calls())
	}
}

// A retry's backoff is not a stall.
func TestClaudeRun_RetryBackoffExtendsWait(t *testing.T) {
	p, calls := countedFakeClaude(t, `echo '{"type":"system","subtype":"api_retry","attempt":5,"max_retries":10,"retry_delay_ms":2000,"error_status":529,"error":"overloaded"}'
sleep 2
`+evResult)
	res, err := p.Run(context.Background(), "prompt", RunOpts{Timeout: 20 * time.Second})
	if err != nil || res.Text != "no findings" || calls() != 1 {
		t.Fatalf("res = %+v, err = %v, calls = %d; want the backoff waited out", res, err, calls())
	}
}

// A tool can run for minutes without events; that is not a stall.
func TestClaudeRun_ToolRunIsNotAStall(t *testing.T) {
	p, calls := countedFakeClaude(t, `echo '{"type":"assistant","message":{"content":[{"type":"tool_use"}]}}'
sleep 2
echo '{"type":"user","message":{"content":[{"type":"tool_result"}]}}'
`+evResult)
	res, err := p.Run(context.Background(), "prompt", RunOpts{Timeout: 20 * time.Second})
	if err != nil || res.Text != "no findings" || calls() != 1 {
		t.Fatalf("res = %+v, err = %v, calls = %d; want the tool waited out", res, err, calls())
	}
}

// Once a tool ran (the fix may have edited files), a stall is reported, not
// rerun.
func TestClaudeRun_StallAfterToolIsNotRetried(t *testing.T) {
	p, calls := countedFakeClaude(t, `echo '{"type":"assistant","message":{"content":[{"type":"tool_use"}]}}'
echo '{"type":"user","message":{"content":[{"type":"tool_result"}]}}'
exec sleep 30`)
	_, err := p.Run(context.Background(), "prompt", RunOpts{Timeout: 20 * time.Second})
	if err == nil {
		t.Fatal("want a stall error")
	}
	want := "claude timed out: the CLI sent nothing for 1s (last event: user, Ns into the call)"
	if got := normalizeElapsed(err.Error()); got != want {
		t.Errorf("error =\n%s\nwant\n%s", got, want)
	}
	if calls() != 1 {
		t.Errorf("CLI ran %d times, want 1", calls())
	}
}

// Parallel tool calls: the timer stays off until every call has its result,
// not just the first.
func TestClaudeRun_ParallelToolsWaitForAllResults(t *testing.T) {
	p, calls := countedFakeClaude(t, `echo '{"type":"assistant","message":{"content":[{"type":"tool_use"},{"type":"tool_use"}]}}'
echo '{"type":"user","message":{"content":[{"type":"tool_result"}]}}'
sleep 2
echo '{"type":"user","message":{"content":[{"type":"tool_result"}]}}'
`+evResult)
	res, err := p.Run(context.Background(), "prompt", RunOpts{Timeout: 20 * time.Second})
	if err != nil || res.Text != "no findings" || calls() != 1 {
		t.Fatalf("res = %+v, err = %v, calls = %d; want the slower tool waited out", res, err, calls())
	}
}

// A message whose content is a plain string still counts as an event.
func TestClaudeStream_StringContentIsAnEvent(t *testing.T) {
	s := &claudeStream{model: "opus", abort: func() {}}
	_, _ = s.Write([]byte(`{"type":"user","message":{"content":"hello"}}` + "\n"))
	if s.lastEvent != "user" || s.toolsRunning != 0 {
		t.Errorf("lastEvent = %q, toolsRunning = %d; want the user event seen", s.lastEvent, s.toolsRunning)
	}
}

// A CLI that sends its result and then hangs on exit is stopped, and the
// result is kept.
func TestClaudeRun_HangAfterResultKeepsResult(t *testing.T) {
	p, calls := countedFakeClaude(t, evResult+`
exec sleep 30`)
	start := time.Now()
	res, err := p.Run(context.Background(), "prompt", RunOpts{Timeout: 20 * time.Second})
	if err != nil || res.Text != "no findings" || calls() != 1 {
		t.Fatalf("res = %+v, err = %v, calls = %d; want the result kept", res, err, calls())
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("Run took %s; want the hang cut at the stall", elapsed)
	}
}
