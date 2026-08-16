package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"

	"github.com/tdeshazo/agentflow-spec/internal/workflow"
)

const runIdentityVersion = 1

// RunIdentity is the durable, non-secret binding between a state namespace and
// the workflow invocation that initialized it. It contains only digests: never
// add resolved parameter values to this record.
type RunIdentity struct {
	Version          int    `json:"version"`
	Algorithm        string `json:"algorithm"`
	WorkflowDigest   string `json:"workflow_digest"`
	ParametersDigest string `json:"parameters_digest"`
	ExecutionDigest  string `json:"execution_digest"`
}

// runWorkflowDefinition deliberately excludes descriptive metadata and the
// source file path. Spec is the executable workflow surface; the workflow name
// already selects a separate Git state namespace.
type runWorkflowDefinition struct {
	APIVersion string        `json:"api_version"`
	Kind       string        `json:"kind"`
	Spec       workflow.Spec `json:"spec"`
}

// runExecutionInputs covers runtime values that are neither part of Spec nor
// declared parameters, but which can still change how the same definition
// executes. Its contents are hashed with the rest of the identity and are not
// written to Git as plaintext.
type runExecutionInputs struct {
	RepositoryRoot string                         `json:"repository_root"`
	WorkflowFile   string                         `json:"workflow_file"`
	Environment    map[string]runEnvironmentValue `json:"environment"`
}

type runEnvironmentValue struct {
	Present bool   `json:"present"`
	Value   string `json:"value,omitempty"`
}

func (e *Engine) expectedRunIdentity() (RunIdentity, error) {
	workflowDigest, err := digestCanonicalJSON(runWorkflowDefinition{
		APIVersion: e.Workflow.APIVersion,
		Kind:       e.Workflow.Kind,
		Spec:       e.Workflow.Spec,
	})
	if err != nil {
		return RunIdentity{}, fmt.Errorf("canonicalize workflow definition: %w", err)
	}
	parametersDigest, err := digestCanonicalJSON(e.Parameters)
	if err != nil {
		return RunIdentity{}, fmt.Errorf("canonicalize resolved run parameters: %w", err)
	}
	executionDigest, err := digestCanonicalJSON(runExecutionInputs{
		RepositoryRoot: e.Repo.Root,
		WorkflowFile:   e.Workflow.File,
		Environment:    e.externalEnvironmentInputs(),
	})
	if err != nil {
		return RunIdentity{}, fmt.Errorf("canonicalize resolved execution inputs: %w", err)
	}
	return RunIdentity{
		Version:          runIdentityVersion,
		Algorithm:        "sha256",
		WorkflowDigest:   workflowDigest,
		ParametersDigest: parametersDigest,
		ExecutionDigest:  executionDigest,
	}, nil
}

func (e *Engine) externalEnvironmentInputs() map[string]runEnvironmentValue {
	names := templateEnvironmentReferences(e.Workflow.Spec)
	for name := range expressionEnvironmentReferences(e.Workflow.Spec) {
		names[name] = struct{}{}
	}
	values := make(map[string]runEnvironmentValue, len(names))
	for name := range names {
		value, present := os.LookupEnv(name)
		values[name] = runEnvironmentValue{Present: present, Value: value}
	}
	return values
}

// templateEnvironmentReferences visits all string-valued executable fields in
// Spec. Environment references only evaluate in ordinary strings when they
// occur inside {{ ... }}, which EnvironmentReferences parses precisely.
func templateEnvironmentReferences(spec workflow.Spec) map[string]struct{} {
	names := map[string]struct{}{}
	// A parameter's selected value is already bound by ParametersDigest. Exclude
	// its default expression here so an unrelated fallback environment change
	// cannot invalidate an explicit --set override of that parameter.
	spec.Parameters = nil
	var visit func(reflect.Value)
	visit = func(value reflect.Value) {
		if !value.IsValid() {
			return
		}
		switch value.Kind() {
		case reflect.Interface, reflect.Pointer:
			if !value.IsNil() {
				visit(value.Elem())
			}
		case reflect.String:
			for _, name := range mustEnvironmentReferences(value.String()) {
				names[name] = struct{}{}
			}
		case reflect.Struct:
			for i := 0; i < value.NumField(); i++ {
				visit(value.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < value.Len(); i++ {
				visit(value.Index(i))
			}
		case reflect.Map:
			for _, key := range value.MapKeys() {
				visit(value.MapIndex(key))
			}
		}
	}
	visit(reflect.ValueOf(spec))
	return names
}

// A workflow passed to Engine.New has already been validated, so any template
// parse failure here is impossible. Treat a defensive parse failure as no
// reference; execution itself will still fail closed if an invalid workflow is
// constructed programmatically.
func mustEnvironmentReferences(value string) []string {
	names, err := workflow.EnvironmentReferences(value)
	if err != nil {
		return nil
	}
	return names
}

// Conditions and loop bounds are evaluated as expressions even when they omit
// interpolation delimiters, so collect their direct env.* references too.
func expressionEnvironmentReferences(spec workflow.Spec) map[string]struct{} {
	names := map[string]struct{}{}
	add := func(value string) {
		for _, name := range mustExpressionEnvironmentReferences(value) {
			names[name] = struct{}{}
		}
	}
	add(spec.State.Reset.When)
	for _, check := range spec.Preconditions {
		add(check.When)
	}
	for _, action := range spec.PhaseDefaults.Before {
		add(action.If)
	}
	for _, action := range spec.PhaseDefaults.After {
		add(action.If)
	}
	for _, phase := range spec.Phases {
		add(phase.If)
		for _, action := range phase.After {
			add(action.If)
		}
	}
	for _, validation := range spec.Validation {
		for _, step := range validation.Steps {
			add(step.If)
		}
		for _, step := range validation.OnFailure.Then {
			add(step.If)
		}
	}
	for _, gate := range spec.HumanGates {
		add(gate.When)
		add(gate.If)
		add(gate.Skip.AllowedWhen)
	}
	for _, step := range spec.Flow {
		add(step.If)
		if step.Loop != nil {
			add(step.Loop.While)
			add(step.Loop.MaxIterations)
		}
	}
	return names
}

func mustExpressionEnvironmentReferences(value string) []string {
	names, err := workflow.ExpressionEnvironmentReferences(value)
	if err != nil {
		return nil
	}
	return names
}

// digestCanonicalJSON uses encoding/json's stable struct field order and
// lexicographically sorted string-map keys. The executable schema and resolved
// parameter values are composed only of JSON-compatible scalar values, slices,
// structs, and string-keyed maps. The canonical bytes exist only in memory;
// durable state receives the SHA-256 digest, never their plaintext values.
func digestCanonicalJSON(value any) (string, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// verifyStoredRunIdentity returns false when the namespace has no identity
// record yet. That permits interrupted-initialization recovery to distinguish a
// legacy/partial state from a fully initialized run. A present record must
// always match before any durable execution evidence is used.
func (e *Engine) verifyStoredRunIdentity() (bool, error) {
	var saved RunIdentity
	ok, err := e.Store.GetJSON(e.runIdentityRecord(), &saved)
	if err != nil || !ok {
		return ok, err
	}
	expected, err := e.expectedRunIdentity()
	if err != nil {
		return false, err
	}
	if saved.Version != expected.Version || saved.Algorithm != expected.Algorithm {
		return false, fmt.Errorf("durable run identity is incompatible with this runtime; reset workflow state before starting a new run")
	}
	if saved.WorkflowDigest != expected.WorkflowDigest {
		return false, fmt.Errorf("durable run identity differs: executable workflow definition changed; reset workflow state before starting a new run")
	}
	if saved.ParametersDigest != expected.ParametersDigest {
		return false, fmt.Errorf("durable run identity differs: resolved run inputs changed; reset workflow state before starting a new run")
	}
	if saved.ExecutionDigest != expected.ExecutionDigest {
		return false, fmt.Errorf("durable run identity differs: resolved execution environment changed; reset workflow state before starting a new run")
	}
	return true, nil
}
