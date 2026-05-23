package main

import "path/filepath"

const workflowLogsDirName = "workflow-logs"

func workflowLogPath(dataDir string, runID string) string {
	return filepath.Join(dataDir, workflowLogsDirName, runID+".log")
}
