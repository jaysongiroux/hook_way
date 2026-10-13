package flowconfig

import (
	"fmt"
	"strings"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
)

// PublishTemporalOptions defines the workflow start independently from the
// destination's connection and RPC deadline. Strings keep the editor's schema
// and exported YAML aligned with Go duration syntax.
type PublishTemporalOptions struct {
	WorkflowType     string                    `yaml:"workflow_type" json:"workflow_type" jsonschema:"required"`
	Input            string                    `yaml:"input,omitempty" json:"input,omitempty" jsonschema:"enum=envelope,enum=body"`
	WorkflowID       TemporalWorkflowIDOptions `yaml:"workflow_id,omitempty" json:"workflow_id,omitempty"`
	IDReusePolicy    string                    `yaml:"id_reuse_policy,omitempty" json:"id_reuse_policy,omitempty" jsonschema:"enum=allow_duplicate,enum=allow_duplicate_failed_only,enum=reject_duplicate,enum=terminate_if_running"`
	ExecutionTimeout string                    `yaml:"execution_timeout,omitempty" json:"execution_timeout,omitempty"`
	RunTimeout       string                    `yaml:"run_timeout,omitempty" json:"run_timeout,omitempty"`
	TaskTimeout      string                    `yaml:"task_timeout,omitempty" json:"task_timeout,omitempty"`
}

type TemporalWorkflowIDOptions struct {
	Source string `yaml:"source,omitempty" json:"source,omitempty" jsonschema:"enum=static,enum=endpoint,enum=header,enum=body"`
	Value  string `yaml:"value,omitempty" json:"value,omitempty"`
	Path   string `yaml:"path,omitempty" json:"path,omitempty"`
	Prefix string `yaml:"prefix,omitempty" json:"prefix,omitempty"`
}

func temporalEndpointOptions(o *PublishTemporalOptions) config.TemporalEndpointOptions {
	if o == nil {
		return config.TemporalEndpointOptions{}
	}
	execution, _ := time.ParseDuration(o.ExecutionTimeout)
	run, _ := time.ParseDuration(o.RunTimeout)
	task, _ := time.ParseDuration(o.TaskTimeout)
	return config.TemporalEndpointOptions{
		WorkflowType: o.WorkflowType, Input: o.Input,
		WorkflowID:    config.TemporalWorkflowIDConfig{Source: o.WorkflowID.Source, Value: o.WorkflowID.Value, Path: o.WorkflowID.Path, Prefix: o.WorkflowID.Prefix},
		IDReusePolicy: o.IDReusePolicy, ExecutionTimeout: execution, RunTimeout: run, TaskTimeout: task,
	}
}

func validatePublishTemporal(o PublishTemporalOptions, target string) error {
	if strings.TrimSpace(o.WorkflowType) == "" {
		return fmt.Errorf("temporal.workflow_type is required")
	}
	for _, timeout := range []struct{ name, value string }{{"execution_timeout", o.ExecutionTimeout}, {"run_timeout", o.RunTimeout}, {"task_timeout", o.TaskTimeout}} {
		if timeout.value == "" {
			continue
		}
		value, err := time.ParseDuration(timeout.value)
		if err != nil || value <= 0 {
			return fmt.Errorf("temporal.%s must be a positive duration (for example 30s or 1h)", timeout.name)
		}
	}
	if err := validateTemplate("temporal.workflow_id.prefix", o.WorkflowID.Prefix); err != nil {
		return err
	}
	issues := config.TemporalOptionsValidationIssues("temporal", target, temporalEndpointOptions(&o))
	if len(issues) > 0 {
		return fmt.Errorf("%s: %s", issues[0].Field, issues[0].Message)
	}
	return nil
}

func temporalPublishIssues(cfg Config) []config.ValidationIssue {
	var issues []config.ValidationIssue
	seen := map[[2]string]config.TemporalEndpointOptions{}
	for _, name := range sortedKeys(cfg.Flows) {
		for _, node := range ExpandedNodes(cfg.Flows[name]) {
			if node.Type != "publish" {
				continue
			}
			opts, err := PublishNodeOptions(node)
			if err != nil {
				continue
			}
			dest, ok := cfg.Destinations[opts.Destination]
			if !ok {
				continue
			}
			field := "flows." + name + ".nodes." + node.ID
			if dest.Type != "temporal" {
				if opts.Temporal != nil {
					issues = append(issues, cfgIssue(field+".temporal", "temporal settings require a temporal destination", ""))
				}
				continue
			}
			if opts.Temporal == nil {
				issues = append(issues, cfgIssue(field+".temporal", "workflow settings are required for a temporal destination", "set temporal.workflow_type to the registered workflow type"))
				continue
			}
			if opts.Create {
				issues = append(issues, cfgIssue(field+".create", "create is not supported for temporal destinations", "workers create task queues by polling; remove create"))
			}
			settings := temporalEndpointOptions(opts.Temporal)
			key := [2]string{opts.Destination, opts.Target}
			if previous, exists := seen[key]; exists && previous != settings {
				issues = append(issues, cfgIssue(field+".temporal", "publish nodes sharing a temporal task queue must agree on workflow settings", "use separate task queues for different workflow settings"))
			}
			seen[key] = settings
		}
	}
	return issues
}
