package gitstate

import "fmt"

// StatusProjection is the generic, non-secret status view used by repository-
// wide inspection. Its acceptance fields come from existing Git-backed
// records, not from the descriptor.
type StatusProjection struct {
	SchemaVersion    int    `json:"schema_version"`
	Namespace        string `json:"namespace,omitempty"`
	Workflow         string `json:"workflow,omitempty"`
	Repo             string `json:"repo,omitempty"`
	Initialized      bool   `json:"initialized"`
	State            string `json:"state"`
	Base             string `json:"base,omitempty"`
	Branch           string `json:"branch,omitempty"`
	Head             string `json:"head,omitempty"`
	ActivePhase      string `json:"active_phase,omitempty"`
	PhaseStartCommit string `json:"phase_start_commit,omitempty"`
	ActorCompleted   bool   `json:"actor_completed"`
	FailureKind      string `json:"failure_kind,omitempty"`
	ValidationFailed string `json:"validation_failed,omitempty"`
	Complete         bool   `json:"complete"`
	CompleteCommit   string `json:"complete_commit,omitempty"`
	ProcessLiveness  string `json:"process_liveness,omitempty"`
	Error            string `json:"error,omitempty"`
}

type projectedActivePhase struct {
	PhaseID        string `json:"phase_id"`
	StartCommit    string `json:"phase_start_commit"`
	ActorCompleted bool   `json:"actor_completed"`
	FailureKind    string `json:"failure_kind,omitempty"`
	Validation     string `json:"validation,omitempty"`
}

// ProjectStatus reads the existing acceptance records named by d. A malformed
// record returns an error so callers can report that namespace independently.
func (d Descriptor) ProjectStatus(repo Repo, namespace string) (StatusProjection, error) {
	if err := d.Validate(d.Workflow); err != nil {
		return StatusProjection{}, err
	}
	base, initialized, err := (Store{Repo: repo, Namespace: namespace}).Resolve(d.Records.Base)
	if err != nil {
		return StatusProjection{}, err
	}
	if initialized && !repo.ObjectExists(base+"^{commit}") {
		return StatusProjection{}, fmt.Errorf("base record %q does not name a commit", d.Records.Base)
	}
	head, err := repo.Head()
	if err != nil {
		return StatusProjection{}, err
	}
	store := Store{Repo: repo, Namespace: namespace}
	var branch string
	branchExists, err := store.GetJSON(d.Records.Branch, &branch)
	if err != nil {
		return StatusProjection{}, err
	}
	var active projectedActivePhase
	activeExists, err := store.GetJSON(d.Records.ActivePhase, &active)
	if err != nil {
		return StatusProjection{}, err
	}
	if activeExists {
		if active.PhaseID == "" {
			return StatusProjection{}, fmt.Errorf("active phase record %q has no phase id", d.Records.ActivePhase)
		}
		if active.StartCommit != "" && !repo.ObjectExists(active.StartCommit+"^{commit}") {
			return StatusProjection{}, fmt.Errorf("active phase record %q has an invalid start commit", d.Records.ActivePhase)
		}
	}
	completeSHA, completeExists, err := store.Resolve(d.Records.WorkflowComplete)
	if err != nil {
		return StatusProjection{}, err
	}
	complete := initialized && completeExists && repo.ObjectExists(completeSHA+"^{commit}") && repo.IsAncestor(completeSHA, "HEAD")

	state := "uninitialized"
	if initialized {
		state = "ready"
	}
	if !initialized && activeExists {
		state = "stale"
	}
	if complete {
		state = "completed"
	}
	if initialized && !complete && activeExists {
		state = "active"
		switch active.FailureKind {
		case "safety":
			state = "safety-failed/terminal"
		case "validation":
			state = "validation-failed/recoverable"
		case "":
			if active.Validation != "" {
				state = "validation-failed/recoverable"
			}
		}
	}

	projection := StatusProjection{
		SchemaVersion: 1,
		Namespace:     namespace,
		Workflow:      d.Workflow,
		Repo:          repo.Root,
		Initialized:   initialized,
		State:         state,
		Head:          head,
		Complete:      complete,
	}
	if initialized {
		projection.Base = base
	}
	if branchExists {
		projection.Branch = branch
	}
	if complete {
		projection.CompleteCommit = completeSHA
	}
	if activeExists {
		projection.ActivePhase = active.PhaseID
		projection.PhaseStartCommit = active.StartCommit
		projection.ActorCompleted = active.ActorCompleted
		projection.FailureKind = active.FailureKind
		projection.ValidationFailed = active.Validation
	}
	return projection, nil
}

// ProjectionError returns a stable status item for a malformed namespace.
func ProjectionError(item Discovery, repo string, err error) StatusProjection {
	return StatusProjection{SchemaVersion: 1, Namespace: item.Namespace, Workflow: item.Workflow, Repo: repo, State: fmt.Sprintf("malformed: %s", err)}
}
