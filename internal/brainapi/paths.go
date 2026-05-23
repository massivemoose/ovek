package brainapi

func JobPath(jobID string) string {
	return "/v1/jobs/" + jobID
}

func JobLogsPath(jobID string) string {
	return JobPath(jobID) + "/logs"
}

func JobLogsStreamPath(jobID string) string {
	return JobLogsPath(jobID) + "/stream"
}

func ProjectWorkflowsPath(projectName string) string {
	return "/v1/projects/" + projectName + "/workflows"
}

func ProjectWorkflowPath(projectName string, workflowName string) string {
	return ProjectWorkflowsPath(projectName) + "/" + workflowName
}

func ProjectWorkflowRunsPath(projectName string) string {
	return "/v1/projects/" + projectName + "/workflow-runs"
}

func ProjectWorkflowRunPath(projectName string, runID string) string {
	return ProjectWorkflowRunsPath(projectName) + "/" + runID
}

func ProjectWorkflowRunLogsPath(projectName string, runID string) string {
	return ProjectWorkflowRunPath(projectName, runID) + "/logs"
}

func ProjectWorkflowRunLogsStreamPath(projectName string, runID string) string {
	return ProjectWorkflowRunLogsPath(projectName, runID) + "/stream"
}

func ProjectWorkflowDefinitionRunsPath(projectName string, workflowName string) string {
	return ProjectWorkflowPath(projectName, workflowName) + "/runs"
}
