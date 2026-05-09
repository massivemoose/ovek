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
