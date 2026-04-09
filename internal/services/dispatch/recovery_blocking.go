package dispatch

import "github.com/syndg/tack/internal/harness/blueprint"

func haltLocalRepairLoop(exec *blueprint.Execution, step *blueprint.Step) {
	if exec == nil || step == nil {
		return
	}
	state := exec.StepStates[step.ID]
	if state == nil {
		return
	}
	state.RetryCount = step.MaxAttempts()
	maxIter := step.MaxFixIterations
	if maxIter <= 0 {
		maxIter = 3
	}
	state.FixIterations = maxIter
}
