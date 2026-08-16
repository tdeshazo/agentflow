package workflow

type Workflow struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       Spec     `yaml:"spec"`
	File       string   `yaml:"-"`
}

type Metadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

type Spec struct {
	Parameters    map[string]Parameter  `yaml:"parameters"`
	Paths         map[string]string     `yaml:"paths"`
	State         StateSpec             `yaml:"state"`
	Workspace     WorkspaceSpec         `yaml:"workspace"`
	Agents        map[string]Agent      `yaml:"agents"`
	Tools         map[string]Tool       `yaml:"tools"`
	Preconditions []Check               `yaml:"preconditions"`
	Progress      ProgressSpec          `yaml:"progress"`
	Validation    map[string]Validation `yaml:"validation"`
	Phases        []Phase               `yaml:"phases"`
	HumanGates    []HumanGate           `yaml:"humanGates"`
	Flow          []FlowStep            `yaml:"flow"`
	Completion    map[string]Completion `yaml:"completion"`
}

type Parameter struct {
	Type    string `yaml:"type"`
	Default any    `yaml:"default"`
	Env     string `yaml:"env"`
}

type StateSpec struct {
	Backend string `yaml:"backend"`
}

type WorkspaceSpec struct {
	Root           string         `yaml:"root"`
	LocalControl   LocalControl   `yaml:"localControlFiles"`
	MutationPolicy MutationPolicy `yaml:"mutationPolicy"`
	Checkpointing  CheckpointSpec `yaml:"checkpointing"`
}

type LocalControl struct {
	Ignored []string `yaml:"ignoredForDirtyState"`
}

type MutationPolicy struct {
	Allowed   []string        `yaml:"allowed"`
	Integrity []IntegrityRule `yaml:"integrity"`
}

type IntegrityRule struct {
	ID        string    `yaml:"id"`
	Paths     []string  `yaml:"paths"`
	Exclude   []string  `yaml:"exclude"`
	Mode      string    `yaml:"mode"`
	Normalize Normalize `yaml:"normalize"`
}

type Normalize struct {
	Command string `yaml:"command"`
}

type CheckpointSpec struct {
	CommitMessage string `yaml:"commit_message"`
}

type Agent struct {
	Runner    string `yaml:"runner"`
	Model     string `yaml:"model"`
	Sandbox   string `yaml:"sandbox"`
	Approval  string `yaml:"approval"`
	Ephemeral bool   `yaml:"ephemeral"`
	Color     string `yaml:"color"`
	MayCommit bool   `yaml:"may_commit"`
}

type Tool struct {
	Type              string         `yaml:"type"`
	Command           string         `yaml:"command"`
	Policy            string         `yaml:"policy"`
	MutatesWorkspace  bool           `yaml:"mutates_workspace"`
	Capture           map[string]any `yaml:"capture"`
	CommitIfDirty     bool           `yaml:"commit_if_dirty"`
	RequireCleanAfter bool           `yaml:"require_clean_after"`
}

type Check struct {
	ID                    string   `yaml:"id"`
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
	Source    ProgressSource    `yaml:"source"`
	Criteria  []Criterion       `yaml:"criteria"`
	Invariant ProgressInvariant `yaml:"criterionPhaseInvariant"`
}

type ProgressSource struct {
	Type             string   `yaml:"type"`
	Path             string   `yaml:"path"`
	UncheckedPattern string   `yaml:"uncheckedPattern"`
	CheckedPattern   string   `yaml:"checkedPattern"`
	CheckedPatterns  []string `yaml:"checkedPatterns"`
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
	Repair    string        `yaml:"repair"`
	Steps     []ToolUse     `yaml:"steps"`
	OnFailure FailurePolicy `yaml:"onFailure"`
	Failure   string        `yaml:"failure"`
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
	Uses string         `yaml:"uses"`
	With map[string]any `yaml:"with"`
}

type Phase struct {
	ID             string `yaml:"id"`
	Kind           string `yaml:"kind"`
	Label          string `yaml:"label"`
	Actor          string `yaml:"actor"`
	Reasoning      string `yaml:"reasoning"`
	Criterion      string `yaml:"criterion"`
	RequiresChange bool   `yaml:"requiresChange"`
	Prompt         string `yaml:"prompt"`
}

type HumanGate struct {
	ID              string          `yaml:"id"`
	When            string          `yaml:"when"`
	Instructions    string          `yaml:"instructions"`
	Checklist       []ChecklistItem `yaml:"checklist"`
	Acknowledgement Acknowledgement `yaml:"acknowledgement"`
	Skip            HumanSkip       `yaml:"skip"`
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
}

type FlowStep struct {
	ID         string         `yaml:"id"`
	If         string         `yaml:"if"`
	Phase      string         `yaml:"phase"`
	Human      string         `yaml:"human"`
	Validate   string         `yaml:"validate"`
	Checkpoint string         `yaml:"checkpoint"`
	Label      string         `yaml:"label"`
	Complete   string         `yaml:"complete"`
	Recover    string         `yaml:"recover"`
	Assert     map[string]any `yaml:"assert"`
	Then       []FlowAction   `yaml:"then"`
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
	WriteMarker               map[string]any `yaml:"writeMarker"`
	Summary                   map[string]any `yaml:"summary"`
}

type CompletionStep struct {
	Uses  string `yaml:"uses"`
	Label string `yaml:"label"`
}

type Assertion struct {
	Type   string         `yaml:"type"`
	Uses   string         `yaml:"uses"`
	Policy string         `yaml:"policy"`
	With   map[string]any `yaml:"with"`
}
