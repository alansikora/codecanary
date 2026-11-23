package review

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
