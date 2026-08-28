package gitstate

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

const (
	// DescriptorRecord is the fixed observational record used to discover a
	// workflow without loading its YAML definition.
	DescriptorRecord = "observability/descriptor"
	DescriptorSchema = 1
)

// RecordNames identifies the existing acceptance records needed for a
// read-only status projection. These names are metadata, never authority.
type RecordNames struct {
	Base                 string `json:"base"`
	Branch               string `json:"branch"`
	ActivePhase          string `json:"active_phase"`
	WorkflowComplete     string `json:"workflow_complete"`
	LastFailure          string `json:"last_failure,omitempty"`
	CompletedPhasePrefix string `json:"completed_phase_prefix,omitempty"`
}

// FailureRecord is bounded diagnostic state. It explains the last failed run
// but never authorizes recovery or acceptance.
type FailureRecord struct {
	Stage string `json:"stage"`
	Error string `json:"error"`
}

// ProcessMetadata is durable only while an AgentFlow process is active. The
// start token prevents a reused PID from being reported as the old process.
type ProcessMetadata struct {
	PID   int    `json:"pid"`
	Start string `json:"start"`
}

// Descriptor is rebuildable observational metadata. It must never be used to
// authorize phase recovery, acceptance, or completion.
type Descriptor struct {
	SchemaVersion int              `json:"schema_version"`
	Workflow      string           `json:"workflow"`
	WorkflowFile  string           `json:"workflow_file,omitempty"`
	Records       RecordNames      `json:"records"`
	Process       *ProcessMetadata `json:"process,omitempty"`
}

// NamespaceForWorkflow returns the injective, path-safe Git namespace for a
// workflow name.
func NamespaceForWorkflow(workflowName string) string {
	return "refs/agentflow/workflow-" + hex.EncodeToString([]byte(workflowName))
}

// WorkflowNameFromNamespace decodes only complete AgentFlow workflow
// namespaces. It rejects path traversal and malformed encodings.
func WorkflowNameFromNamespace(namespace string) (string, error) {
	const prefix = "refs/agentflow/workflow-"
	if !strings.HasPrefix(namespace, prefix) {
		return "", fmt.Errorf("not an AgentFlow workflow namespace: %q", namespace)
	}
	remainder := strings.TrimPrefix(namespace, prefix)
	if remainder == "" || strings.ContainsAny(remainder, "/\\") || len(remainder)%2 != 0 {
		return "", fmt.Errorf("malformed AgentFlow workflow namespace: %q", namespace)
	}
	b, err := hex.DecodeString(remainder)
	if err != nil {
		return "", fmt.Errorf("decode AgentFlow workflow namespace: %w", err)
	}
	name := string(b)
	if name == "" || strings.ContainsRune(name, '\x00') {
		return "", fmt.Errorf("invalid workflow name in namespace: %q", namespace)
	}
	return name, nil
}

// Discovery is one namespace returned by DiscoverDescriptors. Error is set
// for deterministic, reportable malformed namespaces and descriptors.
type Discovery struct {
	Namespace  string
	Workflow   string
	Descriptor *Descriptor
	Error      string
}

// DiscoverDescriptors finds every workflow namespace, retaining malformed
// entries so one bad namespace cannot hide another workflow.
func (r Repo) DiscoverDescriptors() ([]Discovery, error) {
	b, err := r.run(nil, "for-each-ref", "--format=%(refname)", "refs/agentflow/")
	if err != nil {
		return nil, err
	}
	roots := map[string]struct{}{}
	for _, ref := range strings.Split(string(b), "\n") {
		ref = strings.TrimSpace(ref)
		if ref == "" || !strings.HasPrefix(ref, "refs/agentflow/") {
			continue
		}
		remainder := strings.TrimPrefix(ref, "refs/agentflow/")
		root := remainder
		if i := strings.IndexByte(root, '/'); i >= 0 {
			root = root[:i]
		}
		roots["refs/agentflow/"+root] = struct{}{}
	}
	namespaces := make([]string, 0, len(roots))
	for namespace := range roots {
		namespaces = append(namespaces, namespace)
	}
	sort.Strings(namespaces)

	out := make([]Discovery, 0, len(namespaces))
	for _, namespace := range namespaces {
		item := Discovery{Namespace: namespace}
		decoded, decodeErr := WorkflowNameFromNamespace(namespace)
		if decodeErr != nil {
			item.Error = decodeErr.Error()
			out = append(out, item)
			continue
		}
		item.Workflow = decoded
		store := Store{Repo: r, Namespace: namespace}
		var descriptor Descriptor
		ok, getErr := store.GetJSON(DescriptorRecord, &descriptor)
		if getErr != nil {
			item.Error = getErr.Error()
			out = append(out, item)
			continue
		}
		if !ok {
			item.Error = "observability descriptor is missing"
			out = append(out, item)
			continue
		}
		if validateErr := descriptor.Validate(decoded); validateErr != nil {
			item.Error = validateErr.Error()
			out = append(out, item)
			continue
		}
		item.Descriptor = &descriptor
		out = append(out, item)
	}
	return out, nil
}

// FindDescriptor returns the descriptor for an exact workflow name.
func (r Repo) FindDescriptor(workflowName string) (Discovery, bool, error) {
	items, err := r.DiscoverDescriptors()
	if err != nil {
		return Discovery{}, false, err
	}
	for _, item := range items {
		if item.Workflow == workflowName {
			return item, true, nil
		}
	}
	return Discovery{}, false, nil
}

// Validate checks only descriptor invariants. It does not inspect or trust
// acceptance records.
func (d Descriptor) Validate(namespaceWorkflow string) error {
	if d.SchemaVersion != DescriptorSchema {
		return fmt.Errorf("unsupported observability descriptor schema version %d", d.SchemaVersion)
	}
	if d.Workflow == "" || d.Workflow != namespaceWorkflow {
		return fmt.Errorf("observability descriptor workflow does not match namespace")
	}
	records := []struct {
		label  string
		record string
	}{
		{label: "base", record: d.Records.Base},
		{label: "branch", record: d.Records.Branch},
		{label: "active phase", record: d.Records.ActivePhase},
		{label: "workflow complete", record: d.Records.WorkflowComplete},
	}
	for _, item := range records {
		if err := validateRecordName(item.record); err != nil {
			return fmt.Errorf("invalid %s record: %w", item.label, err)
		}
	}
	if d.Records.LastFailure != "" {
		if err := validateRecordName(d.Records.LastFailure); err != nil {
			return fmt.Errorf("invalid last failure record: %w", err)
		}
	}
	if d.Process != nil && (d.Process.PID <= 0 || d.Process.Start == "") {
		return fmt.Errorf("invalid process metadata")
	}
	return nil
}

func validateRecordName(name string) error {
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\\x00") {
		return fmt.Errorf("record name is not a safe relative name")
	}
	if name == DescriptorRecord {
		return fmt.Errorf("record name is reserved for observability metadata")
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("record name contains a path traversal component")
		}
	}
	return nil
}

// NewDescriptor creates metadata with the runtime's default record names
// filled in, including workflow-configurable names where present.
func NewDescriptor(workflowName, workflowFile string, records RecordNames) Descriptor {
	if records.Base == "" {
		records.Base = "base"
	}
	if records.Branch == "" {
		records.Branch = "branch"
	}
	if records.ActivePhase == "" {
		records.ActivePhase = "active"
	}
	if records.WorkflowComplete == "" {
		records.WorkflowComplete = "complete"
	}
	return Descriptor{SchemaVersion: DescriptorSchema, Workflow: workflowName, WorkflowFile: workflowFile, Records: records}
}
