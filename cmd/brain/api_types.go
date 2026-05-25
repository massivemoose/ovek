package main

import "github.com/massivemoose/ovek/internal/brainapi"

type apiError = brainapi.APIError
type projectSummary = brainapi.ProjectSummary
type deploymentRecord = brainapi.Deployment
type jobLinks = brainapi.JobLinks
type job = brainapi.Job
type workflowLinks = brainapi.WorkflowLinks
type workflowDefinition = brainapi.Workflow
type workflowRunLinks = brainapi.WorkflowRunLinks
type workflowRun = brainapi.WorkflowRun
type upsertWorkflowRequest = brainapi.UpsertWorkflowRequest
type createWorkflowRunRequest = brainapi.CreateWorkflowRunRequest
type createDeploymentRequest = brainapi.CreateDeploymentRequest
type createRunRequest = brainapi.CreateRunRequest
type projectRuntimeApp = brainapi.ProjectRuntimeApp
type projectRuntimeContainer = brainapi.ProjectRuntimeContainer
type projectRuntimeNetwork = brainapi.ProjectRuntimeNetwork
type projectRuntimeView = brainapi.ProjectRuntime
