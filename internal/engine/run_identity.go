package engine

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"

	"github.com/tdeshazo/agentflow/internal/workflow"
)

const (
	legacyRunIdentityVersion = 1
	runIdentityVersion       = 2
)

// RunIdentity is the durable, non-secret binding between a state namespace and
// the workflow invocation that initialized it. Besides its opaque ID it
// contains only digests; never add resolved parameter values to this record.
type RunIdentity struct {
	Version          int    `json:"version"`
	RunID            string `json:"run_id,omitempty"`
	Algorithm        string `json:"algorithm"`
	WorkflowDigest   string `json:"workflow_digest"`
	ParametersDigest string `json:"parameters_digest"`
	ExecutionDigest  string `json:"execution_digest"`
}

// runWorkflowDefinition deliberately excludes descriptive metadata and the
// source file path. Spec is the executable workflow surface; the workflow name
// already selects a separate Git state namespace.
type runWorkflowDefinition struct {
	APIVersion      string                         `json:"api_version"`
	Kind            string                         `json:"kind"`
	Spec            any                            `json:"spec"`
	DependencyGraph *workflow.PhaseDependencyGraph `json:"dependency_graph,omitempty"`
}

// legacyRunWorkflowSpec is the v1alpha1 identity shape before runtime-owned
// lifecycle and authoring defaults were introduced. It lets an active legacy
// workflow resume when it does not use any of those newer capabilities.
type legacyRunWorkflowSpec struct {
	Parameters    map[string]workflow.Parameter  `json:"Parameters"`
	Paths         map[string]string              `json:"Paths"`
	State         workflow.StateSpec             `json:"State"`
	Workspace     workflow.WorkspaceSpec         `json:"Workspace"`
	Agents        map[string]workflow.Agent      `json:"Agents"`
	Tools         map[string]workflow.Tool       `json:"Tools"`
	Temp          workflow.TempSpec              `json:"Temp"`
	Preconditions []workflow.Check               `json:"Preconditions"`
	Progress      workflow.ProgressSpec          `json:"Progress"`
	Validation    map[string]legacyValidation    `json:"Validation"`
	PhaseDefaults legacyPhaseDefaults            `json:"PhaseDefaults"`
	Phases        []legacyPhase                  `json:"Phases"`
	HumanGates    []workflow.HumanGate           `json:"HumanGates"`
	Recovery      legacyRecovery                 `json:"Recovery"`
	Flow          []workflow.FlowStep            `json:"Flow"`
	Completion    map[string]workflow.Completion `json:"Completion"`
}

type legacyValidation struct {
	Repair    string                 `json:"Repair"`
	Steps     []workflow.ToolUse     `json:"Steps"`
	OnFailure workflow.FailurePolicy `json:"OnFailure"`
	Failure   string                 `json:"Failure"`
}

type legacyPhaseDefaults struct {
	Before []legacyPhaseAction `json:"Before"`
	After  []legacyPhaseAction `json:"After"`
	Skip   workflow.PhaseSkip  `json:"Skip"`
}

type legacyPhaseAction struct {
	RequireCleanImplementationWorkspace      bool                        `json:"RequireCleanImplementationWorkspace"`
	CapturePhaseStartCommit                  bool                        `json:"CapturePhaseStartCommit"`
	CaptureUncheckedCountBefore              bool                        `json:"CaptureUncheckedCountBefore"`
	PersistActivePhase                       workflow.PersistActivePhase `json:"PersistActivePhase"`
	Validate                                 string                      `json:"Validate"`
	If                                       string                      `json:"If"`
	AssertProgress                           *workflow.ProgressAssertion `json:"AssertProgress"`
	Checkpoint                               string                      `json:"Checkpoint"`
	AssertNetRepositoryChangeSincePhaseStart bool                        `json:"AssertNetRepositoryChangeSincePhaseStart"`
	MarkPhaseComplete                        *workflow.Marker            `json:"MarkPhaseComplete"`
	ClearActivePhase                         bool                        `json:"ClearActivePhase"`
	Return                                   string                      `json:"Return"`
	AssertProgressIfApplicable               bool                        `json:"AssertProgressIfApplicable"`
	MarkPhaseCompleteFlag                    bool                        `json:"MarkPhaseCompleteFlag"`
	RunRepairPolicy                          string                      `json:"RunRepairPolicy"`
	IfStillIncomplete                        workflow.IncompleteAction   `json:"IfStillIncomplete"`
}

type legacyPhase struct {
	ID             string              `json:"ID"`
	Kind           string              `json:"Kind"`
	Label          string              `json:"Label"`
	Actor          string              `json:"Actor"`
	Reasoning      string              `json:"Reasoning"`
	Criterion      string              `json:"Criterion"`
	RequiresChange bool                `json:"RequiresChange"`
	If             string              `json:"If"`
	Prompt         string              `json:"Prompt"`
	After          []legacyPhaseAction `json:"After"`
}

type legacyRecovery struct {
	ActivePhase []legacyRecoveryAction `json:"ActivePhase"`
}

type legacyRecoveryAction struct {
	ReadActivePhase                  workflow.PersistActivePhase `json:"ReadActivePhase"`
	RestorePhaseDefinition           bool                        `json:"RestorePhaseDefinition"`
	RestorePhaseDefinitionFlag       bool                        `json:"RestorePhaseDefinitionFlag"`
	AssertPhaseStartCommitExists     bool                        `json:"AssertPhaseStartCommitExists"`
	AssertPhaseStartIsAncestorOfHead bool                        `json:"AssertPhaseStartIsAncestorOfHead"`
	If                               string                      `json:"If"`
	Then                             []legacyPhaseAction         `json:"Then"`
	RunAgent                         workflow.RunAgent           `json:"RunAgent"`
	RunPhaseAfterSequence            bool                        `json:"RunPhaseAfterSequence"`
	Assert                           string                      `json:"Assert"`
	ValidateExistingStateFirst       bool                        `json:"ValidateExistingStateFirst"`
	IfValid                          []legacyPhaseAction         `json:"IfValid"`
	IfInvalid                        []legacyPhaseAction         `json:"IfInvalid"`
}

// runWorkflowSpec is the durable identity form of workflow.Spec. Keep the
// legacy fields in their original order so adding an optional authoring
// convenience does not invalidate an active workflow that does not use it.
// Defaults is included only when it has executable content.
type runWorkflowSpec struct {
	Parameters      map[string]workflow.Parameter  `json:"Parameters"`
	Paths           map[string]string              `json:"Paths"`
	State           workflow.StateSpec             `json:"State"`
	Workspace       workflow.WorkspaceSpec         `json:"Workspace"`
	Agents          map[string]workflow.Agent      `json:"Agents"`
	Tools           map[string]workflow.Tool       `json:"Tools"`
	Temp            workflow.TempSpec              `json:"Temp"`
	Preconditions   []workflow.Check               `json:"Preconditions"`
	Progress        workflow.ProgressSpec          `json:"Progress"`
	Validation      map[string]workflow.Validation `json:"Validation"`
	Lifecycle       *workflow.LifecyclePolicy      `json:"Lifecycle,omitempty"`
	PhaseDefaults   workflow.PhaseDefaults         `json:"PhaseDefaults"`
	Phases          []workflow.Phase               `json:"Phases"`
	HumanGates      []workflow.HumanGate           `json:"HumanGates"`
	Recovery        workflow.Recovery              `json:"Recovery"`
	Flow            []workflow.FlowStep            `json:"Flow"`
	Completion      map[string]workflow.Completion `json:"Completion"`
	Contracts       workflow.ContractSpec          `json:"Contracts"`
	Criteria        workflow.CriteriaSpec          `json:"Criteria"`
	Defaults        *workflow.AuthoringDefaults    `json:"Defaults,omitempty"`
	ExecutionPolicy *workflow.ExecutionPolicy      `json:"ExecutionPolicy,omitempty"`
}

func runIdentitySpec(spec workflow.Spec) any {
	if usesLegacyIdentityShape(spec) {
		return legacyRunIdentitySpec(spec)
	}
	identity := runWorkflowSpec{
		Parameters:    spec.Parameters,
		Paths:         spec.Paths,
		State:         spec.State,
		Workspace:     spec.Workspace,
		Agents:        spec.Agents,
		Tools:         spec.Tools,
		Temp:          spec.Temp,
		Preconditions: spec.Preconditions,
		Progress:      spec.Progress,
		Validation:    spec.Validation,
		PhaseDefaults: spec.PhaseDefaults,
		Phases:        spec.Phases,
		HumanGates:    spec.HumanGates,
		Recovery:      spec.Recovery,
		Flow:          spec.Flow,
		Completion:    spec.Completion,
		Contracts:     spec.Contracts,
		Criteria:      spec.Criteria,
	}
	if !reflect.DeepEqual(spec.Lifecycle, workflow.LifecyclePolicy{}) {
		lifecycle := spec.Lifecycle
		identity.Lifecycle = &lifecycle
	}
	if !reflect.DeepEqual(spec.Defaults, workflow.AuthoringDefaults{}) {
		defaults := spec.Defaults
		identity.Defaults = &defaults
	}
	if !reflect.DeepEqual(spec.Execution.Policy, workflow.ExecutionPolicy{}) {
		policy := spec.Execution.Policy
		identity.ExecutionPolicy = &policy
	}
	return identity
}

func usesLegacyIdentityShape(spec workflow.Spec) bool {
	if !reflect.DeepEqual(spec.Lifecycle, workflow.LifecyclePolicy{}) || !reflect.DeepEqual(spec.Defaults, workflow.AuthoringDefaults{}) || !reflect.DeepEqual(spec.Execution.Policy, workflow.ExecutionPolicy{}) {
		return false
	}
	for _, validation := range spec.Validation {
		if len(validation.Dependencies) != 0 {
			return false
		}
	}
	if !legacyPhaseActions(spec.PhaseDefaults.Before) || !legacyPhaseActions(spec.PhaseDefaults.After) {
		return false
	}
	for _, phase := range spec.Phases {
		if phase.CriterionID != "" || phase.AdvanceProgress || len(phase.Bookkeeping) != 0 || phase.Validation != "" || !legacyPhaseActions(phase.After) {
			return false
		}
	}
	for _, action := range spec.Recovery.ActivePhase {
		if !legacyPhaseActions(action.Then) || !legacyPhaseActions(action.IfValid) || !legacyPhaseActions(action.IfInvalid) {
			return false
		}
	}
	return true
}

func legacyPhaseActions(actions []workflow.PhaseAction) bool {
	for _, action := range actions {
		if action.AssertProgressUnchanged || action.AdvanceProgress || action.ApplyBookkeeping {
			return false
		}
	}
	return true
}

func legacyRunIdentitySpec(spec workflow.Spec) legacyRunWorkflowSpec {
	validation := make(map[string]legacyValidation, len(spec.Validation))
	for id, value := range spec.Validation {
		validation[id] = legacyValidation{
			Repair:    value.Repair,
			Steps:     value.Steps,
			OnFailure: value.OnFailure,
			Failure:   value.Failure,
		}
	}
	return legacyRunWorkflowSpec{
		Parameters:    spec.Parameters,
		Paths:         spec.Paths,
		State:         spec.State,
		Workspace:     spec.Workspace,
		Agents:        spec.Agents,
		Tools:         spec.Tools,
		Temp:          spec.Temp,
		Preconditions: spec.Preconditions,
		Progress:      spec.Progress,
		Validation:    validation,
		PhaseDefaults: legacyPhaseDefaultsFrom(spec.PhaseDefaults),
		Phases:        legacyPhases(spec.Phases),
		HumanGates:    spec.HumanGates,
		Recovery:      legacyRecoveryFrom(spec.Recovery),
		Flow:          spec.Flow,
		Completion:    spec.Completion,
	}
}

func legacyPhaseDefaultsFrom(defaults workflow.PhaseDefaults) legacyPhaseDefaults {
	return legacyPhaseDefaults{
		Before: legacyPhaseActionsFrom(defaults.Before),
		After:  legacyPhaseActionsFrom(defaults.After),
		Skip:   defaults.Skip,
	}
}

func legacyPhases(phases []workflow.Phase) []legacyPhase {
	legacy := make([]legacyPhase, len(phases))
	for i, phase := range phases {
		legacy[i] = legacyPhase{
			ID:             phase.ID,
			Kind:           phase.Kind,
			Label:          phase.Label,
			Actor:          phase.Actor,
			Reasoning:      phase.Reasoning,
			Criterion:      phase.Criterion,
			RequiresChange: phase.RequiresChange,
			If:             phase.If,
			Prompt:         phase.Prompt,
			After:          legacyPhaseActionsFrom(phase.After),
		}
	}
	return legacy
}

func legacyPhaseActionsFrom(actions []workflow.PhaseAction) []legacyPhaseAction {
	if actions == nil {
		return nil
	}
	legacy := make([]legacyPhaseAction, len(actions))
	for i, action := range actions {
		legacy[i] = legacyPhaseAction{
			RequireCleanImplementationWorkspace:      action.RequireCleanImplementationWorkspace,
			CapturePhaseStartCommit:                  action.CapturePhaseStartCommit,
			CaptureUncheckedCountBefore:              action.CaptureUncheckedCountBefore,
			PersistActivePhase:                       action.PersistActivePhase,
			Validate:                                 action.Validate,
			If:                                       action.If,
			AssertProgress:                           action.AssertProgress,
			Checkpoint:                               action.Checkpoint,
			AssertNetRepositoryChangeSincePhaseStart: action.AssertNetRepositoryChangeSincePhaseStart,
			MarkPhaseComplete:                        action.MarkPhaseComplete,
			ClearActivePhase:                         action.ClearActivePhase,
			Return:                                   action.Return,
			AssertProgressIfApplicable:               action.AssertProgressIfApplicable,
			MarkPhaseCompleteFlag:                    action.MarkPhaseCompleteFlag,
			RunRepairPolicy:                          action.RunRepairPolicy,
			IfStillIncomplete:                        action.IfStillIncomplete,
		}
	}
	return legacy
}

func legacyRecoveryFrom(recovery workflow.Recovery) legacyRecovery {
	legacy := legacyRecovery{ActivePhase: make([]legacyRecoveryAction, len(recovery.ActivePhase))}
	for i, action := range recovery.ActivePhase {
		legacy.ActivePhase[i] = legacyRecoveryAction{
			ReadActivePhase:                  action.ReadActivePhase,
			RestorePhaseDefinition:           action.RestorePhaseDefinition,
			RestorePhaseDefinitionFlag:       action.RestorePhaseDefinitionFlag,
			AssertPhaseStartCommitExists:     action.AssertPhaseStartCommitExists,
			AssertPhaseStartIsAncestorOfHead: action.AssertPhaseStartIsAncestorOfHead,
			If:                               action.If,
			Then:                             legacyPhaseActionsFrom(action.Then),
			RunAgent:                         action.RunAgent,
			RunPhaseAfterSequence:            action.RunPhaseAfterSequence,
			Assert:                           action.Assert,
			ValidateExistingStateFirst:       action.ValidateExistingStateFirst,
			IfValid:                          legacyPhaseActionsFrom(action.IfValid),
			IfInvalid:                        legacyPhaseActionsFrom(action.IfInvalid),
		}
	}
	return legacy
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
	identityWorkflow := e.Workflow
	if e.identityWorkflow != nil {
		identityWorkflow = e.identityWorkflow
	}
	definition := runWorkflowDefinition{
		APIVersion: identityWorkflow.APIVersion,
		Kind:       identityWorkflow.Kind,
		Spec:       runIdentitySpec(identityWorkflow.Spec),
	}
	if usesDependencySchedule(identityWorkflow.APIVersion) {
		graph := identityWorkflow.DependencyGraph
		definition.DependencyGraph = &graph
	}
	workflowDigest, err := digestCanonicalJSON(definition)
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
	for _, credential := range e.Workflow.Spec.Execution.Policy.Credentials {
		names[credential.Env] = struct{}{}
	}
	for _, agent := range e.Workflow.Spec.Agents {
		if agent.Policy == nil {
			continue
		}
		for _, credential := range agent.Policy.Credentials {
			names[credential.Env] = struct{}{}
		}
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
	if (saved.Version != legacyRunIdentityVersion && saved.Version != expected.Version) || saved.Algorithm != expected.Algorithm {
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
	if saved.Version == legacyRunIdentityVersion {
		saved.Version = runIdentityVersion
		saved.RunID, err = newStableID("run")
		if err != nil {
			return false, err
		}
		if err := e.Store.SetJSON(e.runIdentityRecord(), saved); err != nil {
			return false, fmt.Errorf("migrate durable run identity: %w", err)
		}
	}
	if saved.RunID == "" {
		return false, fmt.Errorf("durable run identity has no stable run id; reset workflow state before starting a new run")
	}
	if !validStableID(saved.RunID, "run") {
		return false, fmt.Errorf("durable run identity has an invalid stable run id; reset workflow state before starting a new run")
	}
	e.runID = saved.RunID
	return true, nil
}

func newStableID(kind string) (string, error) {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", fmt.Errorf("generate stable %s identity: %w", kind, err)
	}
	return kind + "_" + hex.EncodeToString(entropy[:]), nil
}

func validStableID(value, kind string) bool {
	prefix := kind + "_"
	if len(value) != len(prefix)+32 || value[:len(prefix)] != prefix {
		return false
	}
	decoded, err := hex.DecodeString(value[len(prefix):])
	return err == nil && len(decoded) == 16
}
