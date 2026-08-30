package workflow

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// This file is the authoritative v1alpha1 document model.  YAML is decoded
// into these types with yaml.Decoder.KnownFields enabled; do not add parallel
// "validation-only" representations of executable fields.

// Workflow is the top-level agentflow document model.
type Workflow struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       Spec     `yaml:"spec"`
	File       string   `yaml:"-"`
	// DependencyGraph is the normalized v1alpha2 execution contract. It is
	// deliberately outside Spec and excluded from YAML so v1alpha1 continues
	// to reject dependsOn rather than silently acquiring a second language.
	DependencyGraph PhaseDependencyGraph `yaml:"-"`
}

// Metadata is descriptive: it names and explains a document but does not
// change orchestration behavior. Every field under Spec is executable unless
// Validate reports it as unsupported by the selected runtime.
type Metadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Source      Source `yaml:"source"`
}

// Source describes the origin of a workflow.
type Source struct {
	Type string `yaml:"type"`
	File string `yaml:"file"`
}

type Spec struct {
	Parameters    map[string]Parameter  `yaml:"parameters"`
	Paths         map[string]string     `yaml:"paths"`
	State         StateSpec             `yaml:"state"`
	Workspace     WorkspaceSpec         `yaml:"workspace"`
	Agents        map[string]Agent      `yaml:"agents"`
	Tools         map[string]Tool       `yaml:"tools"`
	Temp          TempSpec              `yaml:"temp"`
	Preconditions []Check               `yaml:"preconditions"`
	Progress      ProgressSpec          `yaml:"progress"`
	Validation    map[string]Validation `yaml:"validation"`
	Lifecycle     LifecyclePolicy       `yaml:"lifecycle"`
	PhaseDefaults PhaseDefaults         `yaml:"phaseDefaults"`
	Phases        []Phase               `yaml:"phases"`
	HumanGates    []HumanGate           `yaml:"humanGates"`
	Recovery      Recovery              `yaml:"recovery"`
	Flow          []FlowStep            `yaml:"flow"`
	Completion    map[string]Completion `yaml:"completion"`
	// Defaults is an authoring convenience. Normalize resolves it before an
	// engine is constructed, so it never weakens the executable contract.
	Defaults AuthoringDefaults `yaml:"defaults"`
}

// AuthoringDefaults contains only inherited capability and lifecycle values.
// It deliberately cannot declare mutation policy, validation steps, or flow:
// those authorities remain explicit in their own sections.
type AuthoringDefaults struct {
	Agent     Agent                    `yaml:"agent"`
	Lifecycle LifecyclePolicy          `yaml:"lifecycle"`
	Phases    map[string]PhaseTemplate `yaml:"phases"`
	Repair    Repair                   `yaml:"repair"`
}

// PhaseTemplate is selected by the phase's explicit kind. Local phase fields
// override it. It is intentionally a subset of Phase's actor-facing fields.
type PhaseTemplate struct {
	Actor          string `yaml:"actor"`
	Reasoning      string `yaml:"reasoning"`
	RequiresChange *bool  `yaml:"requiresChange"`
	Validation     string `yaml:"validation"`
}

// LifecyclePolicy describes the runtime-owned lifecycle for mutable AI
// phases. It is intentionally small: validation remains a named deterministic
// workflow concern, while the runtime owns the safe phase boundaries around
// it. An omitted policy uses the runtime's safe-resume defaults when a phase
// has no legacy procedural lifecycle actions.
type LifecyclePolicy struct {
	Policy     string `yaml:"policy"`
	Validation string `yaml:"validation"`
	Checkpoint string `yaml:"checkpoint"`
}

type Parameter struct {
	Type    string `yaml:"type"`
	Default any    `yaml:"default"`
	Env     string `yaml:"env"`
}
type StateSpec struct {
	Backend    string          `yaml:"backend"`
	Directory  string          `yaml:"directory"`
	Records    StateRecords    `yaml:"records"`
	Initialize StateInitialize `yaml:"initialize"`
	Reset      StateReset      `yaml:"reset"`
	Lineage    StateLineage    `yaml:"lineage"`
	Resume     StateResume     `yaml:"resume"`
}
type StateRecords struct {
	BaseCommit            string            `yaml:"base_commit"`
	Branch                string            `yaml:"branch"`
	ActivePhase           string            `yaml:"active_phase"`
	CompletedPhasePattern string            `yaml:"completed_phase_pattern"`
	CompletedPhases       string            `yaml:"completed_phases"`
	ManualConfirmation    string            `yaml:"manual_confirmation"`
	HumanVerification     string            `yaml:"human_verification"`
	WorkflowComplete      string            `yaml:"workflow_complete"`
	Integrity             map[string]string `yaml:"integrity"`
}
type StateInitialize struct {
	RequireCleanWorkspace               bool `yaml:"require_clean_workspace"`
	RequireCleanImplementationWorkspace bool `yaml:"require_clean_implementation_workspace"`
	RequireNamedBranch                  bool `yaml:"require_named_branch"`
	Capture                             any  `yaml:"capture"`
}
type StateReset struct {
	// Allowed is an authoring-contract projection used by v1alpha2. It is not
	// YAML-addressable in the shared legacy model, so v1alpha1 remains grammar
	// frozen while the runtime can still enforce an explicit reset policy.
	Allowed                             *bool  `yaml:"-"`
	When                                string `yaml:"when"`
	RequireCleanWorkspace               bool   `yaml:"require_clean_workspace"`
	RequireCleanImplementationWorkspace bool   `yaml:"require_clean_implementation_workspace"`
	DeleteStateDirectory                bool   `yaml:"delete_state_directory"`
}
type StateLineage struct {
	RequireBaseCommitExists     bool `yaml:"require_base_commit_exists"`
	RequireBaseIsAncestorOfHead bool `yaml:"require_base_is_ancestor_of_head"`
	RequireSameNamedBranch      bool `yaml:"require_same_named_branch"`
}
type StateResume struct {
	Enabled                     *bool             `yaml:"enabled"`
	RequireBaseIsAncestorOfHead bool              `yaml:"require_base_is_ancestor_of_head"`
	RequireSameBranch           bool              `yaml:"require_same_branch"`
	CompletedPhaseMarker        MarkerPolicy      `yaml:"completed_phase_marker"`
	ActivePhase                 ActivePhasePolicy `yaml:"active_phase"`
}
type MarkerPolicy struct {
	Value                           string `yaml:"value"`
	ValidWhenMarkerCommitExists     bool   `yaml:"valid_when_marker_commit_exists"`
	ValidWhenMarkerIsAncestorOfHead bool   `yaml:"valid_when_marker_is_ancestor_of_head"`
}
type ActivePhasePolicy struct {
	Strategy                          string   `yaml:"strategy"`
	Fields                            []string `yaml:"fields"`
	RequirePhaseStartCommitExists     bool     `yaml:"require_phase_start_commit_exists"`
	RequirePhaseStartIsAncestorOfHead bool     `yaml:"require_phase_start_is_ancestor_of_head"`
	PreservePartialCommits            bool     `yaml:"preserve_partial_commits"`
	PreservePartialWorktreeChanges    bool     `yaml:"preserve_partial_worktree_changes"`
	PreserveExistingChanges           bool     `yaml:"preserve_existing_changes"`
	RerunAgentOnlyIfNeeded            bool     `yaml:"rerun_agent_only_if_needed"`
}

type WorkspaceSpec struct {
	Root           string         `yaml:"root"`
	VCS            string         `yaml:"vcs"`
	LocalControl   LocalControl   `yaml:"localControlFiles"`
	Cleanliness    Cleanliness    `yaml:"cleanliness"`
	AgentCommits   AgentCommits   `yaml:"agent_commits"`
	MutationPolicy MutationPolicy `yaml:"mutationPolicy"`
	Checkpointing  CheckpointSpec `yaml:"checkpointing"`
}
type LocalControl struct {
	Ignored []string `yaml:"ignoredForDirtyState"`
	Note    string   `yaml:"note"`
}
type Cleanliness struct {
	BeforeFirstRun                        string `yaml:"before_first_run"`
	BeforeEachNewPhase                    string `yaml:"before_each_new_phase"`
	BeforeNewPhase                        string `yaml:"before_new_phase"`
	OutsideRecoverableActivePhase         string `yaml:"outside_recoverable_active_phase"`
	AfterCheckpoint                       string `yaml:"after_checkpoint"`
	AllowDirtyOnlyWhenResumingActivePhase bool   `yaml:"allow_dirty_only_when_resuming_active_phase"`
}
type AgentCommits struct {
	Allowed                      bool   `yaml:"allowed"`
	CheckpointUncommittedSuccess bool   `yaml:"checkpoint_uncommitted_success"`
	RequireCleanAfterCheckpoint  bool   `yaml:"require_clean_after_checkpoint"`
	CommitMessage                string `yaml:"commit_message"`
}
type MutationPolicy struct {
	Allowed             []string        `yaml:"allowed"`
	IgnoredControlFiles []string        `yaml:"ignoredControlFiles"`
	Lineage             MutationLineage `yaml:"lineage"`
	Integrity           []IntegrityRule `yaml:"integrity"`
}
type MutationLineage struct {
	RequireBaseIsAncestorOfHead bool `yaml:"require_base_is_ancestor_of_head"`
	RequireSameBranchAsState    bool `yaml:"require_same_branch_as_state"`
}
type IntegrityRule struct {
	ID                     string    `yaml:"id"`
	Paths                  []string  `yaml:"paths"`
	Exclude                []string  `yaml:"exclude"`
	Mode                   string    `yaml:"mode"`
	AllowedSemanticChanges []string  `yaml:"allowed_semantic_changes"`
	Normalize              Normalize `yaml:"normalize"`
}
type Normalize struct {
	Command string `yaml:"command"`
}
type CheckpointSpec struct {
	AgentCommitsAllowed          bool   `yaml:"agent_commits_allowed"`
	CheckpointUncommittedSuccess bool   `yaml:"checkpoint_uncommitted_success"`
	StageOnlyAllowedDirtyFiles   bool   `yaml:"stage_only_allowed_dirty_files"`
	CommitIfDirty                bool   `yaml:"commit_if_dirty"`
	CommitMessage                string `yaml:"commit_message"`
	RequireCleanAfter            bool   `yaml:"require_clean_after"`
	AssertScopeBeforeAndAfter    bool   `yaml:"assert_scope_before_and_after"`
}

type Agent struct {
	Runner            string `yaml:"runner"`
	Model             string `yaml:"model"`
	Sandbox           string `yaml:"sandbox"`
	Approval          string `yaml:"approval"`
	Ephemeral         bool   `yaml:"ephemeral"`
	Color             string `yaml:"-"` // Internal source compatibility only; not workflow schema.
	MayCommit         bool   `yaml:"may_commit"`
	OutputLastMessage bool   `yaml:"output_last_message"`
	present           map[string]bool
}

func (a *Agent) UnmarshalYAML(n *yaml.Node) error {
	type plain Agent
	var out plain
	if err := decodeKnownNode(n, &out); err != nil {
		return err
	}
	out.present = nodeFields(n)
	*a = Agent(out)
	return nil
}

type Tool struct {
	Type              string  `yaml:"type"`
	Command           string  `yaml:"command"`
	Policy            string  `yaml:"policy"`
	Stage             string  `yaml:"stage"`
	MutatesWorkspace  bool    `yaml:"mutates_workspace"`
	Capture           Capture `yaml:"capture"`
	CommitIfDirty     bool    `yaml:"commit_if_dirty"`
	RequireCleanAfter bool    `yaml:"require_clean_after"`
}
type Capture struct {
	Stdout bool   `yaml:"stdout"`
	Stderr bool   `yaml:"stderr"`
	Log    string `yaml:"log"`
}
type TempSpec struct {
	Directory string `yaml:"directory"`
	Cleanup   string `yaml:"cleanup"`
}

type Check struct {
	ID                    string   `yaml:"id"`
	Scope                 string   `yaml:"scope"`
	When                  string   `yaml:"when"`
	Type                  string   `yaml:"type"`
	Path                  string   `yaml:"path"`
	Text                  string   `yaml:"text"`
	Paths                 []string `yaml:"paths"`
	Commands              []string `yaml:"commands"`
	Object                string   `yaml:"object"`
	Base                  string   `yaml:"base"`
	Ancestor              string   `yaml:"ancestor"`
	Descendant            string   `yaml:"descendant"`
	Expected              string   `yaml:"expected"`
	RequireAncestorOfHead bool     `yaml:"require_ancestor_of_head"`
	RequireBranch         string   `yaml:"require_branch"`
	Policy                string   `yaml:"policy"`
}
type ProgressSpec struct {
	Source         ProgressSource    `yaml:"source"`
	Selection      Selection         `yaml:"selection"`
	Criteria       []Criterion       `yaml:"criteria"`
	Invariant      ProgressInvariant `yaml:"criterionPhaseInvariant"`
	PhaseInvariant ProgressInvariant `yaml:"phaseInvariant"`
}
type ProgressSource struct {
	Type             string   `yaml:"type"`
	Path             string   `yaml:"path"`
	UncheckedPattern string   `yaml:"uncheckedPattern"`
	CheckedPattern   string   `yaml:"checkedPattern"`
	CheckedPatterns  []string `yaml:"checkedPatterns"`
}
type Selection struct {
	Strategy string `yaml:"strategy"`
}
type Criterion struct {
	ID   string `yaml:"id"`
	Text string `yaml:"text"`
}
type ProgressInvariant struct {
	TargetedMustBeChecked bool `yaml:"targeted_item_must_be_checked"`
	UncheckedCountDelta   int  `yaml:"unchecked_count_delta"`
	NoOtherMayClose       bool `yaml:"no_other_criterion_may_close"`
}

type Validation struct {
	Repair       string        `yaml:"repair"`
	Steps        []ToolUse     `yaml:"steps"`
	Dependencies []string      `yaml:"dependencies"`
	OnFailure    FailurePolicy `yaml:"onFailure"`
	Failure      string        `yaml:"failure"`
}
type FailurePolicy struct {
	Strategy          string    `yaml:"strategy"`
	MaxRepairAttempts int       `yaml:"maxRepairAttempts"`
	Repair            Repair    `yaml:"repair"`
	Then              []ToolUse `yaml:"then"`
	Exhausted         string    `yaml:"exhausted"`
}
type Repair struct {
	Actor     string `yaml:"actor"`
	Reasoning string `yaml:"reasoning"`
	Prompt    string `yaml:"prompt"`
}
type ToolUse struct {
	Uses string        `yaml:"uses"`
	If   string        `yaml:"if"`
	With ToolArguments `yaml:"with"`
}

// ToolArguments is intentionally small: the executable core currently only
// accepts file-regex arguments. Adding a new tool argument is a schema change,
// not an invitation for YAML keys to be ignored.
type ToolArguments struct {
	Path  string `yaml:"path"`
	Regex string `yaml:"regex"`
}

type PhaseDefaults struct {
	Before []PhaseAction `yaml:"before"`
	After  []PhaseAction `yaml:"after"`
	Skip   PhaseSkip     `yaml:"skip"`
}
type PhaseSkip struct {
	CompletedMarker         string        `yaml:"completedMarker"`
	CriterionAlreadyChecked CriterionSkip `yaml:"criterionAlreadyChecked"`
}
type CriterionSkip struct {
	Action                string `yaml:"action"`
	ValidateBeforeMarking bool   `yaml:"validate_before_marking"`
}
type PhaseAction struct {
	RequireCleanImplementationWorkspace      bool               `yaml:"require_clean_implementation_workspace"`
	CapturePhaseStartCommit                  bool               `yaml:"capture_phase_start_commit"`
	CaptureUncheckedCountBefore              bool               `yaml:"capture_unchecked_count_before"`
	PersistActivePhase                       PersistActivePhase `yaml:"persist_active_phase"`
	Validate                                 string             `yaml:"validate"`
	If                                       string             `yaml:"if"`
	AssertProgress                           *ProgressAssertion `yaml:"assertProgress"`
	Checkpoint                               string             `yaml:"checkpoint"`
	AssertNetRepositoryChangeSincePhaseStart bool               `yaml:"assertNetRepositoryChangeSincePhaseStart"`
	MarkPhaseComplete                        *Marker            `yaml:"markPhaseComplete"`
	ClearActivePhase                         bool               `yaml:"clearActivePhase"`
	Return                                   string             `yaml:"return"`
	AssertProgressIfApplicable               bool               `yaml:"assert_progress_if_applicable"`
	AssertProgressUnchanged                  bool               `yaml:"-"`
	AdvanceProgress                          bool               `yaml:"-"`
	ApplyBookkeeping                         bool               `yaml:"-"`
	MarkPhaseCompleteFlag                    bool               `yaml:"mark_phase_complete"`
	RunRepairPolicy                          string             `yaml:"run_repair_policy"`
	IfStillIncomplete                        IncompleteAction   `yaml:"if_still_incomplete"`
}

// ProgressAssertion is deliberately tied to the workflow progress source. It
// does not accept arbitrary expressions or mutation hooks: it can only assert
// the selected criterion and the documented progress delta.
type ProgressAssertion struct {
	Enabled              bool   `yaml:"-"`
	Criterion            string `yaml:"criterion"`
	UncheckedCountBefore string `yaml:"uncheckedCountBefore"`
	UncheckedCountDelta  int    `yaml:"uncheckedCountDelta"`
}

// UnmarshalYAML accepts the historical `assertProgress: true` spelling while
// normalizing it to the explicit assertion object used by the runtime.
func (p *ProgressAssertion) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		var enabled bool
		if err := value.Decode(&enabled); err != nil {
			return err
		}
		p.Enabled = enabled
		return nil
	}
	type raw ProgressAssertion
	var decoded raw
	if err := value.Decode(&decoded); err != nil {
		return err
	}
	*p = ProgressAssertion(decoded)
	p.Enabled = true
	return nil
}

type PersistActivePhase struct {
	Fields []string `yaml:"fields"`
}
type IncompleteAction struct {
	RerunSameAgentPhaseWithPrefix string `yaml:"rerun_same_agent_phase_with_prefix"`
}
type Marker struct {
	Value  string `yaml:"value"`
	Record string `yaml:"record"`
}
type Phase struct {
	ID        string `yaml:"id"`
	Kind      string `yaml:"kind"`
	Label     string `yaml:"label"`
	Actor     string `yaml:"actor"`
	Reasoning string `yaml:"reasoning"`
	// Criterion is the legacy criterion selector. New documents should use
	// CriterionID so a phase does not need to repeat mutable display text.
	Criterion   string `yaml:"criterion"`
	CriterionID string `yaml:"criterionID"`
	// AdvanceProgress makes the runtime, after deterministic validation, perform
	// the one declared checklist transition. The actor is not allowed to edit
	// declared progress state when this is enabled.
	AdvanceProgress bool `yaml:"advanceProgress"`
	// Bookkeeping contains engine-only, fully declarative Markdown transitions.
	// It is meaningful only on a bookkeeping phase with no actor.
	Bookkeeping    []MarkdownTransition `yaml:"bookkeeping"`
	RequiresChange bool                 `yaml:"requiresChange"`
	Validation     string               `yaml:"validation"`
	If             string               `yaml:"if"`
	Prompt         string               `yaml:"prompt"`
	After          []PhaseAction        `yaml:"after"`
	present        map[string]bool
}

func (p *Phase) UnmarshalYAML(n *yaml.Node) error {
	type plain Phase
	var out plain
	if err := decodeKnownNode(n, &out); err != nil {
		return err
	}
	out.present = nodeFields(n)
	*p = Phase(out)
	return nil
}

// MarkdownTransition is a constrained, byte-preserving Markdown mutation. It
// changes exactly one checklist/index item or one labelled status line; the
// surrounding document is left byte-for-byte intact.
type MarkdownTransition struct {
	Type  string `yaml:"type"`
	Path  string `yaml:"path"`
	Item  string `yaml:"item"`
	State string `yaml:"state"`
	Label string `yaml:"label"`
	From  string `yaml:"from"`
	To    string `yaml:"to"`
}

type HumanGate struct {
	ID               string          `yaml:"id"`
	IdempotentRecord string          `yaml:"idempotent_record"`
	After            []HumanAfter    `yaml:"after"`
	Requires         []string        `yaml:"requires"`
	When             string          `yaml:"when"`
	If               string          `yaml:"if"`
	Instructions     string          `yaml:"instructions"`
	Checklist        []ChecklistItem `yaml:"checklist"`
	Acknowledgement  Acknowledgement `yaml:"acknowledgement"`
	Evidence         Marker          `yaml:"evidence"`
	Skip             HumanSkip       `yaml:"skip"`
}
type HumanAfter struct {
	Phase      string `yaml:"phase"`
	Validation string `yaml:"validation"`
}
type ChecklistItem struct {
	ID   string `yaml:"id"`
	Text string `yaml:"text"`
}
type Acknowledgement struct {
	Type  string `yaml:"type"`
	Value string `yaml:"value"`
}
type HumanSkip struct {
	AllowedWhen string `yaml:"allowed_when"`
	Warning     string `yaml:"warning"`
	Record      string `yaml:"record"`
	Evidence    Marker `yaml:"evidence"`
}

type Recovery struct {
	ActivePhase []RecoveryAction `yaml:"activePhase"`
}
type RecoveryAction struct {
	ReadActivePhase                  PersistActivePhase `yaml:"readActivePhase"`
	RestorePhaseDefinition           bool               `yaml:"restorePhaseDefinition"`
	RestorePhaseDefinitionFlag       bool               `yaml:"restore_phase_definition"`
	AssertPhaseStartCommitExists     bool               `yaml:"assertPhaseStartCommitExists"`
	AssertPhaseStartIsAncestorOfHead bool               `yaml:"assertPhaseStartIsAncestorOfHead"`
	If                               string             `yaml:"if"`
	Then                             []PhaseAction      `yaml:"then"`
	RunAgent                         RunAgent           `yaml:"runAgent"`
	RunPhaseAfterSequence            bool               `yaml:"runPhaseAfterSequence"`
	Assert                           string             `yaml:"assert"`
	ValidateExistingStateFirst       bool               `yaml:"validate_existing_state_first"`
	IfValid                          []PhaseAction      `yaml:"if_valid"`
	IfInvalid                        []PhaseAction      `yaml:"if_invalid"`
}
type RunAgent struct {
	Phase        string `yaml:"phase"`
	PromptPrefix string `yaml:"promptPrefix"`
}

type FlowStep struct {
	ID         string       `yaml:"id"`
	If         string       `yaml:"if"`
	Phase      string       `yaml:"phase"`
	Human      string       `yaml:"human"`
	Validate   string       `yaml:"validate"`
	Checkpoint string       `yaml:"checkpoint"`
	Label      string       `yaml:"label"`
	Complete   string       `yaml:"complete"`
	Recover    string       `yaml:"recover"`
	Assert     *Assertion   `yaml:"assert"`
	Loop       *Loop        `yaml:"loop"`
	Then       []FlowAction `yaml:"then"`
}

// Loop is the only dynamic iteration construct in v1alpha1. It repeatedly
// selects the next unchecked progress item and dispatches a declared phase.
// There is intentionally no arbitrary collection iteration or evaluation.
type Loop struct {
	While                      string            `yaml:"while"`
	MaxIterations              string            `yaml:"maxIterations"`
	Select                     string            `yaml:"select"`
	DispatchByCriterion        map[string]string `yaml:"dispatchByCriterion"`
	RequireUncheckedCountDelta int               `yaml:"requireUncheckedCountDelta"`
}
type FlowAction struct {
	Report string `yaml:"report"`
	Stop   string `yaml:"stop"`
}
type Completion struct {
	Assertions                []Assertion    `yaml:"assertions"`
	FinalValidation           string         `yaml:"finalValidation"`
	Checkpoint                CompletionStep `yaml:"checkpoint"`
	AfterCheckpointAssertions []Assertion    `yaml:"afterCheckpointAssertions"`
	WriteMarker               Marker         `yaml:"writeMarker"`
	Summary                   Summary        `yaml:"summary"`
}
type CompletionStep struct {
	Uses  string `yaml:"uses"`
	Label string `yaml:"label"`
}
type Summary struct {
	Title   string   `yaml:"title"`
	Include []string `yaml:"include"`
}
type Assertion struct {
	Type     string        `yaml:"type"`
	Uses     string        `yaml:"uses"`
	Policy   string        `yaml:"policy"`
	Progress string        `yaml:"progress"`
	With     ToolArguments `yaml:"with"`
}

// Checklist entries may use either the compact string form or the explicit
// id/text form. Both remain part of this schema.
func (c *ChecklistItem) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		c.Text = n.Value
		return nil
	}
	type plain ChecklistItem
	var out plain
	if err := decodeKnownNode(n, &out); err != nil {
		return err
	}
	*c = ChecklistItem(out)
	return nil
}

// checkpoint accepts the legacy shorthand tool name as well as the structured
// uses/label form.
func (c *CompletionStep) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		c.Uses = n.Value
		return nil
	}
	type plain CompletionStep
	var out plain
	if err := decodeKnownNode(n, &out); err != nil {
		return err
	}
	*c = CompletionStep(out)
	return nil
}

func decodeKnownNode(n *yaml.Node, out any) error {
	b, err := yaml.Marshal(n)
	if err != nil {
		return err
	}
	d := yaml.NewDecoder(bytes.NewReader(b))
	d.KnownFields(true)
	if err := d.Decode(out); err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	return nil
}

func nodeFields(n *yaml.Node) map[string]bool {
	out := map[string]bool{}
	if n.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		out[n.Content[i].Value] = true
	}
	return out
}
