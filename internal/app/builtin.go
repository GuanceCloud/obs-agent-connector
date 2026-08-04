package app

import (
	"fmt"
	"strings"

	agent "github.com/GuanceCloud/obs-agent-connector/internal/agent"
	telemetryinstall "github.com/GuanceCloud/obs-agent-connector/internal/install"
)

func installBuiltinAdapter(p agent.Definition, input installInput, noConfig bool) error {
	executable, err := currentExecutable()
	if err != nil {
		return fmt.Errorf("resolve connector executable: %w", err)
	}
	printSingleDetail("Runtime", executable)
	if p.Name != "codebuddy" {
		return fmt.Errorf("%s does not have a built-in telemetry adapter", p.Name)
	}
	_, err = telemetryinstall.InstallCodeBuddy(telemetryinstall.CodeBuddyOptions{
		SourceExecutable: executable, DestinationExecutable: executable,
		Endpoint: input.Endpoint, TracePath: input.TracePath, MetricsPath: input.MetricsPath,
		InstallType: fixedType, XToken: input.XToken, Headers: append([]string{}, input.Headers...),
		ResourceAttributes: builtinResourceAttributes(input), CaptureContent: input.CaptureContent,
		MaxChars: input.MaxChars, Enabled: input.Enabled, NoConfig: noConfig,
	})
	if err == nil {
		printSingleDetail("Note", "Restart CodeBuddy if the reconciled Hook is not picked up automatically.")
	}
	if err != nil {
		return fmt.Errorf("install built-in %s adapter: %w", p.Name, err)
	}
	return nil
}

func removeBuiltinAdapter(p agent.Definition, purgeConfig, purgeState bool) error {
	result, err := telemetryinstall.RemoveAdapter(p.Name, "", telemetryinstall.RemoveOptions{
		PurgeConfig: purgeConfig,
		PurgeState:  purgeState,
	})
	if err != nil {
		return err
	}
	printSingleDetail("Hook", removedOrKept(result.HookRemoved))
	printSingleDetail("Config", removedOrKept(result.ConfigRemoved))
	if purgeState {
		printSingleDetail("State", removedOrKept(result.StatePurged))
	}
	return nil
}

func removedOrKept(removed bool) string {
	if removed {
		return "removed"
	}
	return "kept"
}

func builtinResourceAttributes(input installInput) []string {
	values := append([]string{}, input.GlobalTags...)
	if strings.TrimSpace(input.AgentID) != "" {
		values = append(values, "agent_id="+input.AgentID)
	}
	if strings.TrimSpace(input.AgentName) != "" {
		values = append(values, "agent_name="+input.AgentName)
	}
	return values
}

func installedPluginVersion(p agent.Definition) string {
	if p.IsBuiltin() {
		return version
	}
	return agent.InstalledVersion(p)
}
