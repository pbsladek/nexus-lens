import type { paths } from "./types";

type HealthResponse =
  paths["/api/v1/health"]["get"]["responses"][200]["content"]["application/json"];
export type AppUser =
  paths["/api/v1/me"]["get"]["responses"][200]["content"]["application/json"];
type LoginRequest =
  paths["/api/v1/session"]["post"]["requestBody"]["content"]["application/json"];
export type LoginResponse =
  paths["/api/v1/session"]["post"]["responses"][200]["content"]["application/json"];
export type NexusInstance =
  paths["/api/v1/admin/nexus-instances"]["get"]["responses"][200]["content"]["application/json"]["instances"][number];
export type IndexingSchedule =
  paths["/api/v1/admin/indexing-schedules"]["get"]["responses"][200]["content"]["application/json"]["schedules"][number];
export type UpsertIndexingScheduleRequest =
  paths["/api/v1/admin/indexing-schedules"]["post"]["requestBody"]["content"]["application/json"];
export type RateLimitSettings =
  paths["/api/v1/admin/nexus/{nexus_instance_id}/rate-limits"]["put"]["requestBody"]["content"]["application/json"];
export type RetentionSettings =
  paths["/api/v1/admin/retention-settings"]["get"]["responses"][200]["content"]["application/json"];
export type StartIndexRunRequest =
  paths["/api/v1/index/runs"]["post"]["requestBody"]["content"]["application/json"];
export type IndexRun =
  paths["/api/v1/index/runs"]["post"]["responses"][202]["content"]["application/json"];
export type IndexRunsResponse =
  paths["/api/v1/index/runs"]["get"]["responses"][200]["content"]["application/json"];
export type IndexTask =
  paths["/api/v1/index/tasks/{task_id}/retry"]["post"]["responses"][200]["content"]["application/json"];
export type IndexTasksResponse =
  paths["/api/v1/index/tasks"]["get"]["responses"][200]["content"]["application/json"];
export type VerificationFinding =
  paths["/api/v1/index/verification/findings/{finding_id}/dismiss"]["post"]["responses"][200]["content"]["application/json"];
export type VerificationFindingsResponse =
  paths["/api/v1/index/verification/findings"]["get"]["responses"][200]["content"]["application/json"];
export type AnalyticsOverview =
  paths["/api/v1/analytics/overview"]["get"]["responses"][200]["content"]["application/json"];
export type RepositoryAnalytics =
  paths["/api/v1/analytics/repositories"]["get"]["responses"][200]["content"]["application/json"]["repositories"][number];
export type ImageAnalytics =
  paths["/api/v1/analytics/images"]["get"]["responses"][200]["content"]["application/json"]["images"][number];
export type TagAnalytics =
  paths["/api/v1/analytics/tags"]["get"]["responses"][200]["content"]["application/json"]["tags"][number];
export type TrendPoint =
  paths["/api/v1/analytics/trends"]["get"]["responses"][200]["content"]["application/json"]["points"][number];
export type TrendTopMover =
  paths["/api/v1/analytics/trends/top-movers"]["get"]["responses"][200]["content"]["application/json"]["movers"][number];
export type CleanupSimulationRequest =
  paths["/api/v1/cleanup/simulations"]["post"]["requestBody"]["content"]["application/json"];
export type CleanupSimulationResult =
  paths["/api/v1/cleanup/simulations"]["post"]["responses"][200]["content"]["application/json"];
export type CleanupSimulationTag = CleanupSimulationResult["candidates"][number];
export type CleanupSimulationWarning = CleanupSimulationResult["warnings"][number];
export type CleanupExecutionRequest =
  paths["/api/v1/cleanup/executions"]["post"]["requestBody"]["content"]["application/json"];
export type CleanupExecution =
  paths["/api/v1/cleanup/executions"]["post"]["responses"][202]["content"]["application/json"] & {
    approved_candidate_count?: number;
    post_delete_reconcile_run_id?: number;
    started_at?: string;
    cancel_requested_at?: string;
    canceled_by?: string;
    locked_by?: string;
    lease_expires_at?: string;
  };
export type CleanupExecutionEvent = {
  id: number;
  cleanup_execution_id: number;
  event_type: string;
  actor?: string;
  repository_name?: string;
  image_name?: string;
  tag?: string;
  tag_id?: number;
  rule_name?: string;
  component_id?: string;
  status?: string;
  message?: string;
  error_class?: string;
  error_code?: string;
  error_message?: string;
  details?: unknown;
  created_at: string;
};
export type PackageFormatRepository =
  paths["/api/v1/packages/repositories"]["get"]["responses"][200]["content"]["application/json"]["repositories"][number];
export type TagPattern =
  paths["/api/v1/admin/tag-patterns"]["get"]["responses"][200]["content"]["application/json"]["patterns"][number];
export type UpsertTagPatternRequest =
  paths["/api/v1/admin/tag-patterns"]["post"]["requestBody"]["content"]["application/json"];
export type TagReclassificationRequest =
  paths["/api/v1/admin/tag-patterns/reclassify"]["post"]["requestBody"]["content"]["application/json"];
export type TagPatternAnalytics =
  paths["/api/v1/analytics/tag-patterns"]["get"]["responses"][200]["content"]["application/json"]["patterns"][number];
export type TagPatternGrowth =
  paths["/api/v1/analytics/tag-patterns/growth"]["get"]["responses"][200]["content"]["application/json"]["growth"][number];
export type TagPatternExample =
  paths["/api/v1/analytics/tag-patterns/examples"]["get"]["responses"][200]["content"]["application/json"]["examples"][number];
export type TagPatternSuggestion =
  paths["/api/v1/analytics/tag-patterns/suggestions"]["get"]["responses"][200]["content"]["application/json"]["suggestions"][number];
export type PauseIndexingRequest =
  NonNullable<
    paths["/api/v1/index/nexus/{nexus_instance_id}/pause"]["post"]["requestBody"]
  >["content"]["application/json"];
export type IndexingControl =
  paths["/api/v1/index/nexus/{nexus_instance_id}/pause"]["post"]["responses"][200]["content"]["application/json"];

type AnalyticsListOptions = {
  nexusInstanceID?: number;
  repositoryID?: number;
  imageID?: number;
  repositoryName?: string;
  patternName?: string;
  metricName?: string;
  startDate?: string;
  endDate?: string;
  search?: string;
  olderThanDays?: number;
  growthWindowDays?: number;
  neverDownloaded?: boolean;
  limit?: number;
};

const defaultBaseURL =
  typeof window === "undefined" ? "" : `${window.location.protocol}//${window.location.hostname}:5601`;

const apiBaseURL = import.meta.env.VITE_API_BASE_URL ?? defaultBaseURL;

export async function getHealth(): Promise<HealthResponse> {
  const response = await fetch(`${apiBaseURL}/api/v1/health`, {
    credentials: "include",
  });
  if (!response.ok) {
    throw new Error(`health request failed with status ${response.status}`);
  }
  return (await response.json()) as HealthResponse;
}

export async function getMe(): Promise<AppUser | null> {
  const response = await fetch(`${apiBaseURL}/api/v1/me`, {
    credentials: "include",
  });
  if (response.status === 401) {
    return null;
  }
  if (!response.ok) {
    throw new Error(`me request failed with status ${response.status}`);
  }
  return (await response.json()) as AppUser;
}

export async function login(credentials: LoginRequest): Promise<LoginResponse> {
  const response = await fetch(`${apiBaseURL}/api/v1/session`, {
    method: "POST",
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(credentials),
  });
  if (!response.ok) {
    throw new Error(`login failed with status ${response.status}`);
  }
  return (await response.json()) as LoginResponse;
}

export async function logout(): Promise<void> {
  const response = await fetch(`${apiBaseURL}/api/v1/session`, {
    method: "DELETE",
    credentials: "include",
  });
  if (!response.ok) {
    throw new Error(`logout failed with status ${response.status}`);
  }
}

export async function listIndexRuns(limit?: number): Promise<IndexRunsResponse> {
  const params = new URLSearchParams();
  if (limit) {
    params.set("limit", String(limit));
  }
  const query = params.toString();
  const response = await fetch(`${apiBaseURL}/api/v1/index/runs${query ? `?${query}` : ""}`, {
    credentials: "include",
  });
  if (!response.ok) {
    throw new Error(`index runs request failed with status ${response.status}`);
  }
  return (await response.json()) as IndexRunsResponse;
}

export async function listNexusInstances(limit?: number): Promise<{ instances: NexusInstance[] }> {
  const params = new URLSearchParams();
  if (limit) {
    params.set("limit", String(limit));
  }
  const query = params.toString();
  const response = await fetch(`${apiBaseURL}/api/v1/admin/nexus-instances${query ? `?${query}` : ""}`, {
    credentials: "include",
  });
  if (!response.ok) {
    throw new Error(`nexus instances request failed with status ${response.status}`);
  }
  return (await response.json()) as { instances: NexusInstance[] };
}

export async function listIndexingSchedules(
  nexusInstanceID?: number,
  limit?: number,
): Promise<{ schedules: IndexingSchedule[] }> {
  const params = new URLSearchParams();
  if (nexusInstanceID && nexusInstanceID > 0) {
    params.set("nexus_instance_id", String(nexusInstanceID));
  }
  if (limit) {
    params.set("limit", String(limit));
  }
  const query = params.toString();
  const response = await fetch(`${apiBaseURL}/api/v1/admin/indexing-schedules${query ? `?${query}` : ""}`, {
    credentials: "include",
  });
  if (!response.ok) {
    throw new Error(`indexing schedules request failed with status ${response.status}`);
  }
  return (await response.json()) as { schedules: IndexingSchedule[] };
}

export async function upsertIndexingSchedule(
  request: UpsertIndexingScheduleRequest,
): Promise<IndexingSchedule> {
  const response = await fetch(`${apiBaseURL}/api/v1/admin/indexing-schedules`, {
    method: "POST",
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
  });
  if (!response.ok) {
    throw new Error(`save indexing schedule failed with status ${response.status}`);
  }
  return (await response.json()) as IndexingSchedule;
}

export async function updateNexusRateLimits(
  nexusInstanceID: number,
  request: RateLimitSettings,
): Promise<IndexingControl> {
  const response = await fetch(`${apiBaseURL}/api/v1/admin/nexus/${nexusInstanceID}/rate-limits`, {
    method: "PUT",
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
  });
  if (!response.ok) {
    throw new Error(`save rate limits failed with status ${response.status}`);
  }
  return (await response.json()) as IndexingControl;
}

export async function getRetentionSettings(): Promise<RetentionSettings> {
  const response = await fetch(`${apiBaseURL}/api/v1/admin/retention-settings`, {
    credentials: "include",
  });
  if (!response.ok) {
    throw new Error(`retention settings request failed with status ${response.status}`);
  }
  return (await response.json()) as RetentionSettings;
}

export async function updateRetentionSettings(
  request: RetentionSettings,
): Promise<RetentionSettings> {
  const response = await fetch(`${apiBaseURL}/api/v1/admin/retention-settings`, {
    method: "PUT",
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
  });
  if (!response.ok) {
    throw new Error(`save retention settings failed with status ${response.status}`);
  }
  return (await response.json()) as RetentionSettings;
}

export async function startIndexRun(request: StartIndexRunRequest): Promise<IndexRun> {
  const response = await fetch(`${apiBaseURL}/api/v1/index/runs`, {
    method: "POST",
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
  });
  if (!response.ok) {
    const body = await response.json().catch(() => null);
    const message =
      body && typeof body === "object" && "message" in body && typeof body.message === "string"
        ? body.message
        : `start index run failed with status ${response.status}`;
    throw new Error(message);
  }
  return (await response.json()) as IndexRun;
}

export async function listIndexTasks(scanRunID?: number): Promise<IndexTasksResponse> {
  const params = new URLSearchParams();
  if (scanRunID && scanRunID > 0) {
    params.set("scan_run_id", String(scanRunID));
  }
  params.set("limit", "100");
  const response = await fetch(`${apiBaseURL}/api/v1/index/tasks?${params.toString()}`, {
    credentials: "include",
  });
  if (!response.ok) {
    throw new Error(`index tasks request failed with status ${response.status}`);
  }
  return (await response.json()) as IndexTasksResponse;
}

export async function retryIndexTask(taskID: number): Promise<IndexTask> {
  const response = await fetch(`${apiBaseURL}/api/v1/index/tasks/${taskID}/retry`, {
    method: "POST",
    credentials: "include",
  });
  if (!response.ok) {
    throw new Error(`retry index task failed with status ${response.status}`);
  }
  return (await response.json()) as IndexTask;
}

export async function cancelIndexRun(scanRunID: number): Promise<IndexRun> {
  const response = await fetch(`${apiBaseURL}/api/v1/index/runs/${scanRunID}/cancel`, {
    method: "POST",
    credentials: "include",
  });
  if (!response.ok) {
    throw new Error(`cancel index run failed with status ${response.status}`);
  }
  return (await response.json()) as IndexRun;
}

export async function listVerificationFindings(options: {
  nexusInstanceID?: number;
  status?: string;
} = {}): Promise<VerificationFindingsResponse> {
  const params = new URLSearchParams();
  if (options.nexusInstanceID && options.nexusInstanceID > 0) {
    params.set("nexus_instance_id", String(options.nexusInstanceID));
  }
  if (options.status) {
    params.set("status", options.status);
  }
  params.set("limit", "100");
  const response = await fetch(
    `${apiBaseURL}/api/v1/index/verification/findings?${params.toString()}`,
    {
      credentials: "include",
    },
  );
  if (!response.ok) {
    throw new Error(`verification findings request failed with status ${response.status}`);
  }
  return (await response.json()) as VerificationFindingsResponse;
}

export async function getAnalyticsOverview(nexusInstanceID?: number): Promise<AnalyticsOverview> {
  const params = new URLSearchParams();
  if (nexusInstanceID && nexusInstanceID > 0) {
    params.set("nexus_instance_id", String(nexusInstanceID));
  }
  const response = await fetch(`${apiBaseURL}/api/v1/analytics/overview?${params.toString()}`, {
    credentials: "include",
  });
  if (!response.ok) {
    throw new Error(`analytics overview request failed with status ${response.status}`);
  }
  return (await response.json()) as AnalyticsOverview;
}

export async function listRepositoryAnalytics(
  options: AnalyticsListOptions = {},
): Promise<{ repositories: RepositoryAnalytics[] }> {
  const response = await fetch(
    `${apiBaseURL}/api/v1/analytics/repositories?${analyticsParams(options).toString()}`,
    { credentials: "include" },
  );
  if (!response.ok) {
    throw new Error(`repository analytics request failed with status ${response.status}`);
  }
  return (await response.json()) as { repositories: RepositoryAnalytics[] };
}

export async function listImageAnalytics(
  options: AnalyticsListOptions = {},
): Promise<{ images: ImageAnalytics[] }> {
  const response = await fetch(
    `${apiBaseURL}/api/v1/analytics/images?${analyticsParams(options).toString()}`,
    { credentials: "include" },
  );
  if (!response.ok) {
    throw new Error(`image analytics request failed with status ${response.status}`);
  }
  return (await response.json()) as { images: ImageAnalytics[] };
}

export async function listTagAnalytics(
  options: AnalyticsListOptions = {},
): Promise<{ tags: TagAnalytics[] }> {
  const response = await fetch(
    `${apiBaseURL}/api/v1/analytics/tags?${analyticsParams(options).toString()}`,
    { credentials: "include" },
  );
  if (!response.ok) {
    throw new Error(`tag analytics request failed with status ${response.status}`);
  }
  return (await response.json()) as { tags: TagAnalytics[] };
}

export async function listTrendSeries(
  options: AnalyticsListOptions = {},
): Promise<{ points: TrendPoint[] }> {
  const response = await fetch(
    `${apiBaseURL}/api/v1/analytics/trends?${analyticsParams(options).toString()}`,
    { credentials: "include" },
  );
  if (!response.ok) {
    throw new Error(`trend series request failed with status ${response.status}`);
  }
  return (await response.json()) as { points: TrendPoint[] };
}

export async function listTrendTopMovers(
  options: AnalyticsListOptions = {},
): Promise<{ movers: TrendTopMover[] }> {
  const response = await fetch(
    `${apiBaseURL}/api/v1/analytics/trends/top-movers?${analyticsParams(options).toString()}`,
    { credentials: "include" },
  );
  if (!response.ok) {
    throw new Error(`trend top movers request failed with status ${response.status}`);
  }
  return (await response.json()) as { movers: TrendTopMover[] };
}

export async function simulateCleanupPolicy(
  request: CleanupSimulationRequest,
): Promise<CleanupSimulationResult> {
  const response = await fetch(`${apiBaseURL}/api/v1/cleanup/simulations`, {
    method: "POST",
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
  });
  if (!response.ok) {
    const body = await response.json().catch(() => null);
    const message =
      body && typeof body === "object" && "message" in body && typeof body.message === "string"
        ? body.message
        : `cleanup simulation failed with status ${response.status}`;
    throw new Error(message);
  }
  return (await response.json()) as CleanupSimulationResult;
}

export async function listPackageFormatRepositories(
  options: AnalyticsListOptions = {},
): Promise<{ repositories: PackageFormatRepository[] }> {
  const response = await fetch(
    `${apiBaseURL}/api/v1/packages/repositories?${analyticsParams(options).toString()}`,
    { credentials: "include" },
  );
  if (!response.ok) {
    throw new Error(`package repositories request failed with status ${response.status}`);
  }
  return (await response.json()) as { repositories: PackageFormatRepository[] };
}

export async function requestCleanupExecution(
  request: CleanupExecutionRequest,
): Promise<CleanupExecution> {
  const response = await fetch(`${apiBaseURL}/api/v1/cleanup/executions`, {
    method: "POST",
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
  });
  if (!response.ok) {
    const body = await response.json().catch(() => null);
    const message =
      body && typeof body === "object" && "message" in body && typeof body.message === "string"
        ? body.message
        : `cleanup execution failed with status ${response.status}`;
    throw new Error(message);
  }
  return (await response.json()) as CleanupExecution;
}

export async function listCleanupExecutions(options: AnalyticsListOptions = {}): Promise<{ executions: CleanupExecution[] }> {
  const params = new URLSearchParams();
  if (options.nexusInstanceID && options.nexusInstanceID > 0) {
    params.set("nexus_instance_id", String(options.nexusInstanceID));
  }
  if (options.limit && options.limit > 0) {
    params.set("limit", String(options.limit));
  }
  const response = await fetch(`${apiBaseURL}/api/v1/cleanup/executions?${params.toString()}`, {
    credentials: "include",
  });
  if (!response.ok) {
    throw new Error(`cleanup executions request failed with status ${response.status}`);
  }
  return (await response.json()) as { executions: CleanupExecution[] };
}

export async function listCleanupExecutionEvents(
  cleanupExecutionID: number,
  limit = 200,
): Promise<{ events: CleanupExecutionEvent[] }> {
  const params = new URLSearchParams({ limit: String(limit) });
  const response = await fetch(
    `${apiBaseURL}/api/v1/cleanup/executions/${cleanupExecutionID}/events?${params.toString()}`,
    { credentials: "include" },
  );
  if (!response.ok) {
    throw new Error(`cleanup execution events request failed with status ${response.status}`);
  }
  return (await response.json()) as { events: CleanupExecutionEvent[] };
}

export async function cancelCleanupExecution(cleanupExecutionID: number): Promise<CleanupExecution> {
  const response = await fetch(`${apiBaseURL}/api/v1/cleanup/executions/${cleanupExecutionID}/cancel`, {
    method: "POST",
    credentials: "include",
  });
  if (!response.ok) {
    throw new Error(`cleanup execution cancel failed with status ${response.status}`);
  }
  return (await response.json()) as CleanupExecution;
}

export async function listTagPatterns(limit?: number): Promise<{ patterns: TagPattern[] }> {
  const params = new URLSearchParams();
  if (limit) {
    params.set("limit", String(limit));
  }
  const query = params.toString();
  const response = await fetch(`${apiBaseURL}/api/v1/admin/tag-patterns${query ? `?${query}` : ""}`, {
    credentials: "include",
  });
  if (!response.ok) {
    throw new Error(`tag patterns request failed with status ${response.status}`);
  }
  return (await response.json()) as { patterns: TagPattern[] };
}

export async function upsertTagPattern(request: UpsertTagPatternRequest): Promise<TagPattern> {
  const response = await fetch(`${apiBaseURL}/api/v1/admin/tag-patterns`, {
    method: "POST",
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
  });
  if (!response.ok) {
    throw new Error(`save tag pattern failed with status ${response.status}`);
  }
  return (await response.json()) as TagPattern;
}

export async function startTagReclassification(request: TagReclassificationRequest): Promise<IndexRun> {
  const response = await fetch(`${apiBaseURL}/api/v1/admin/tag-patterns/reclassify`, {
    method: "POST",
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
  });
  if (!response.ok) {
    throw new Error(`start tag reclassification failed with status ${response.status}`);
  }
  return (await response.json()) as IndexRun;
}

export async function listTagPatternAnalytics(
  options: AnalyticsListOptions = {},
): Promise<{ patterns: TagPatternAnalytics[] }> {
  const response = await fetch(
    `${apiBaseURL}/api/v1/analytics/tag-patterns?${analyticsParams(options).toString()}`,
    { credentials: "include" },
  );
  if (!response.ok) {
    throw new Error(`tag pattern analytics request failed with status ${response.status}`);
  }
  return (await response.json()) as { patterns: TagPatternAnalytics[] };
}

export async function listTagPatternGrowth(
  options: AnalyticsListOptions = {},
): Promise<{ growth: TagPatternGrowth[] }> {
  const response = await fetch(
    `${apiBaseURL}/api/v1/analytics/tag-patterns/growth?${analyticsParams(options).toString()}`,
    { credentials: "include" },
  );
  if (!response.ok) {
    throw new Error(`tag pattern growth request failed with status ${response.status}`);
  }
  return (await response.json()) as { growth: TagPatternGrowth[] };
}

export async function listTagPatternExamples(
  options: AnalyticsListOptions = {},
): Promise<{ examples: TagPatternExample[] }> {
  const response = await fetch(
    `${apiBaseURL}/api/v1/analytics/tag-patterns/examples?${analyticsParams(options).toString()}`,
    { credentials: "include" },
  );
  if (!response.ok) {
    throw new Error(`tag pattern examples request failed with status ${response.status}`);
  }
  return (await response.json()) as { examples: TagPatternExample[] };
}

export async function listTagPatternSuggestions(
  options: AnalyticsListOptions = {},
): Promise<{ suggestions: TagPatternSuggestion[] }> {
  const response = await fetch(
    `${apiBaseURL}/api/v1/analytics/tag-patterns/suggestions?${analyticsParams(options).toString()}`,
    { credentials: "include" },
  );
  if (!response.ok) {
    throw new Error(`tag pattern suggestions request failed with status ${response.status}`);
  }
  return (await response.json()) as { suggestions: TagPatternSuggestion[] };
}

export async function dismissVerificationFinding(findingID: number): Promise<VerificationFinding> {
  const response = await fetch(
    `${apiBaseURL}/api/v1/index/verification/findings/${findingID}/dismiss`,
    {
      method: "POST",
      credentials: "include",
    },
  );
  if (!response.ok) {
    throw new Error(`dismiss verification finding failed with status ${response.status}`);
  }
  return (await response.json()) as VerificationFinding;
}

export async function pauseNexusIndexing(
  nexusInstanceID: number,
  request: PauseIndexingRequest,
): Promise<IndexingControl> {
  const response = await fetch(`${apiBaseURL}/api/v1/index/nexus/${nexusInstanceID}/pause`, {
    method: "POST",
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
  });
  if (!response.ok) {
    throw new Error(`pause indexing failed with status ${response.status}`);
  }
  return (await response.json()) as IndexingControl;
}

function analyticsParams(options: AnalyticsListOptions): URLSearchParams {
  const params = new URLSearchParams();
  if (options.nexusInstanceID && options.nexusInstanceID > 0) {
    params.set("nexus_instance_id", String(options.nexusInstanceID));
  }
  if (options.repositoryID && options.repositoryID > 0) {
    params.set("repository_id", String(options.repositoryID));
  }
  if (options.imageID && options.imageID > 0) {
    params.set("image_id", String(options.imageID));
  }
  if (options.repositoryName) {
    params.set("repository_name", options.repositoryName);
  }
  if (options.patternName) {
    params.set("pattern_name", options.patternName);
  }
  if (options.metricName) {
    params.set("metric_name", options.metricName);
  }
  if (options.startDate) {
    params.set("start_date", options.startDate);
  }
  if (options.endDate) {
    params.set("end_date", options.endDate);
  }
  if (options.search) {
    params.set("search", options.search);
  }
  if (options.olderThanDays && options.olderThanDays > 0) {
    params.set("older_than_days", String(options.olderThanDays));
  }
  if (options.growthWindowDays && options.growthWindowDays > 0) {
    params.set("growth_window_days", String(options.growthWindowDays));
  }
  if (options.neverDownloaded) {
    params.set("never_downloaded", "true");
  }
  params.set("limit", String(options.limit && options.limit > 0 ? options.limit : 50));
  return params;
}

export async function resumeNexusIndexing(nexusInstanceID: number): Promise<IndexingControl> {
  const response = await fetch(`${apiBaseURL}/api/v1/index/nexus/${nexusInstanceID}/resume`, {
    method: "POST",
    credentials: "include",
  });
  if (!response.ok) {
    throw new Error(`resume indexing failed with status ${response.status}`);
  }
  return (await response.json()) as IndexingControl;
}
