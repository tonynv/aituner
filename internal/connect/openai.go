package connect

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// openaiGeneric is for any tool that speaks the OpenAI API: nothing to install, just the connection details.
type openaiGeneric struct{}

func (openaiGeneric) ID() string { return "openai" }

func envFilePath(env Env) string { return filepath.Join(env.ConfigDir, "openai.env") }

func (openaiGeneric) Plan(env Env) Plan {
	return Plan{
		ID: "openai", Title: "Any OpenAI-compatible tool",
		Summary: "Connection details for tools that speak the OpenAI API (Continue, Cline, Aider, Open WebUI, scripts, curl).",
		Files:   []string{envFilePath(env)},
		Steps: []Step{
			{Kind: "write", Title: "Write an environment file", Detail: "OPENAI_BASE_URL, OPENAI_API_KEY and OPENAI_MODEL you can `source`"},
			{Kind: "info", Title: "Verify", Detail: "sends a real chat request through the gateway"},
		},
		WillNotTouch: []string{"every other tool and config on this machine"},
	}
}

func (openaiGeneric) Status(ctx context.Context, env Env) Status {
	return Status{Installed: true, Configured: isManaged(envFilePath(env)), Files: []string{envFilePath(env)}}
}

func (openaiGeneric) Setup(ctx context.Context, env Env, emit Emit) error {
	content := "# " + Marker + ": openai-compatible connection to the local model\n" +
		"# usage: source " + shq(envFilePath(env)) + "\n" +
		"export OPENAI_BASE_URL=" + shq(env.BaseURL) + "\n" +
		"export OPENAI_API_BASE=" + shq(env.BaseURL) + "\n" +
		"export OPENAI_API_KEY=" + shq(env.Key) + "\n" +
		"export OPENAI_MODEL=" + shq(env.Model) + "\n"
	if _, err := writeManaged(envFilePath(env), content, 0o600); err != nil {
		return err
	}
	emit("wrote " + envFilePath(env))
	model, err := Probe(ctx, env, true)
	if err != nil {
		emit("Note: end-to-end check skipped: " + err.Error())
		return nil
	}
	emit("Verified: a real chat request through the gateway worked for " + model)
	return nil
}

func (openaiGeneric) Remove(ctx context.Context, env Env, emit Emit) error {
	if ok, err := removeManaged(envFilePath(env)); err != nil {
		return err
	} else if ok {
		emit("removed " + envFilePath(env))
	}
	return nil
}

func (openaiGeneric) Launch(ctx context.Context, env Env, project string) (string, error) {
	return "", fmt.Errorf("nothing to launch: use the connection details in your tool")
}

// CurlExample is shown in the UI.
func CurlExample(env Env) string {
	return strings.Join([]string{
		"curl " + env.BaseURL + "/chat/completions \\",
		"  -H 'Authorization: Bearer $(cat " + env.KeyFile + ")' \\",
		"  -H 'Content-Type: application/json' \\",
		"  -d '{\"model\":\"" + env.Model + "\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello\"}]}'",
	}, "\n")
}
