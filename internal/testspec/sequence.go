package testspec

const MaxSequenceDelayMS uint64 = 86_400_000

// UsesSequenceControls distinguishes the opt-in execution contract from legacy
// sequences, whose timeout continues to cover the entire Case.
func (spec Spec) UsesSequenceControls() bool {
	if spec.Workflow == nil || spec.Workflow.Mode != "sequence" {
		return false
	}
	for _, step := range spec.Workflow.Steps {
		if step.DelayMS > 0 || step.ReuseRequestFrom != "" {
			return true
		}
	}
	return false
}

// ExecutionTimeoutMS accepts a validated Spec and RunSettings timeout. Controlled
// sequences allow one timeout budget per HTTP request, plus their declared waits.
func (spec Spec) ExecutionTimeoutMS(requestTimeoutMS uint64) uint64 {
	if requestTimeoutMS == 0 {
		requestTimeoutMS = 600_000
	}
	if !spec.UsesSequenceControls() {
		return requestTimeoutMS
	}
	total := requestTimeoutMS * uint64(1+len(spec.Workflow.Steps))
	for _, step := range spec.Workflow.Steps {
		total += step.DelayMS
	}
	return total
}
