import type { paths } from "./types";

type HealthResponse =
  paths["/api/v1/health"]["get"]["responses"][200]["content"]["application/json"];
type StatusResponse =
  paths["/api/v1/admin/status"]["get"]["responses"][200]["content"]["application/json"];
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
  paths["/api/v1/cleanup/executions"]["get"]["responses"][200]["content"]["application/json"]["executions"][number];
export type CleanupExecutionEvent =
  paths["/api/v1/cleanup/executions/{cleanup_execution_id}/events"]["get"]["responses"][200]["content"]["application/json"]["events"][number];
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

type ProblemResponse = {
  message?: string;
  detail?: string;
  code?: string;
  request_id?: string;
  retryable?: boolean;
};

export class APIError extends Error {
  readonly status: number;
  readonly code?: string;
  readonly requestID?: string;
  readonly retryable?: boolean;

  constructor(message: string, options: {
    status: number;
    code?: string;
    requestID?: string;
    retryable?: boolean;
  }) {
    super(message);
    this.name = "APIError";
    this.status = options.status;
    this.code = options.code;
    this.requestID = options.requestID;
    this.retryable = options.retryable;
  }
}

export function displayError(error: unknown, fallback: string): string {
  if (error instanceof APIError) {
    const details = [
      error.code ? `code ${error.code}` : "",
      error.requestID ? `request ${error.requestID}` : "",
      error.retryable === undefined ? "" : `retryable ${String(error.retryable)}`,
    ].filter(Boolean);
    return details.length > 0 ? `${error.message} (${details.join(", ")})` : error.message;
  }
  if (error instanceof Error) {
    return error.message;
  }
  return fallback;
}

async function apiJSON<T>(path: string, fallback: string, init: RequestInit = {}): Promise<T> {
  const response = await apiFetch(path, fallback, init);
  return (await response.json()) as T;
}

async function apiVoid(path: string, fallback: string, init: RequestInit = {}): Promise<void> {
  await apiFetch(path, fallback, init);
}

async function apiFetch(path: string, fallback: string, init: RequestInit = {}): Promise<Response> {
  const response = await fetch(`${apiBaseURL}${path}`, {
    credentials: "include",
    ...init,
  });
  if (!response.ok) {
    throw await problemError(response, fallback);
  }
  return response;
}

export async function getHealth(): Promise<HealthResponse> {
  return apiJSON<HealthResponse>("/api/v1/health", "health request failed");
}

export async function getMe(): Promise<AppUser | null> {
  const response = await fetch(`${apiBaseURL}/api/v1/me`, {
    credentials: "include",
  });
  if (response.status === 401) {
    return null;
  }
  if (!response.ok) {
    throw await problemError(response, `me request failed with status ${response.status}`);
  }
  return (await response.json()) as AppUser;
}

export async function login(credentials: LoginRequest): Promise<LoginResponse> {
  return apiJSON<LoginResponse>("/api/v1/session", "login failed", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(credentials),
  });
}

export async function logout(): Promise<void> {
  return apiVoid("/api/v1/session", "logout failed", {
    method: "DELETE",
  });
}

export async function getAdminStatus(): Promise<StatusResponse> {
  return apiJSON<StatusResponse>("/api/v1/admin/status", "admin status request failed");
}

export async function listIndexRuns(limit?: number): Promise<IndexRunsResponse> {
  const params = new URLSearchParams();
  if (limit) {
    params.set("limit", String(limit));
  }
  const query = params.toString();
  return apiJSON<IndexRunsResponse>(
    `/api/v1/index/runs${query ? `?${query}` : ""}`,
    "index runs request failed",
  );
}

export async function listNexusInstances(limit?: number): Promise<{ instances: NexusInstance[] }> {
  const params = new URLSearchParams();
  if (limit) {
    params.set("limit", String(limit));
  }
  const query = params.toString();
  return apiJSON<{ instances: NexusInstance[] }>(
    `/api/v1/admin/nexus-instances${query ? `?${query}` : ""}`,
    "nexus instances request failed",
  );
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
  return apiJSON<{ schedules: IndexingSchedule[] }>(
    `/api/v1/admin/indexing-schedules${query ? `?${query}` : ""}`,
    "indexing schedules request failed",
  );
}

export async function upsertIndexingSchedule(
  request: UpsertIndexingScheduleRequest,
): Promise<IndexingSchedule> {
  return apiJSON<IndexingSchedule>("/api/v1/admin/indexing-schedules", "save indexing schedule failed", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
  });
}

export async function updateNexusRateLimits(
  nexusInstanceID: number,
  request: RateLimitSettings,
): Promise<IndexingControl> {
  return apiJSON<IndexingControl>(
    `/api/v1/admin/nexus/${nexusInstanceID}/rate-limits`,
    "save rate limits failed",
    {
    method: "PUT",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
    },
  );
}

export async function getRetentionSettings(): Promise<RetentionSettings> {
  return apiJSON<RetentionSettings>("/api/v1/admin/retention-settings", "retention settings request failed");
}

export async function updateRetentionSettings(
  request: RetentionSettings,
): Promise<RetentionSettings> {
  return apiJSON<RetentionSettings>("/api/v1/admin/retention-settings", "save retention settings failed", {
    method: "PUT",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
  });
}

export async function startIndexRun(request: StartIndexRunRequest): Promise<IndexRun> {
  return apiJSON<IndexRun>("/api/v1/index/runs", "start index run failed", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
  });
}

export async function listIndexTasks(scanRunID?: number): Promise<IndexTasksResponse> {
  const params = new URLSearchParams();
  if (scanRunID && scanRunID > 0) {
    params.set("scan_run_id", String(scanRunID));
  }
  params.set("limit", "100");
  return apiJSON<IndexTasksResponse>(
    `/api/v1/index/tasks?${params.toString()}`,
    "index tasks request failed",
  );
}

export async function retryIndexTask(taskID: number): Promise<IndexTask> {
  return apiJSON<IndexTask>(`/api/v1/index/tasks/${taskID}/retry`, "retry index task failed", {
    method: "POST",
  });
}

export async function cancelIndexRun(scanRunID: number): Promise<IndexRun> {
  return apiJSON<IndexRun>(`/api/v1/index/runs/${scanRunID}/cancel`, "cancel index run failed", {
    method: "POST",
  });
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
  return apiJSON<VerificationFindingsResponse>(
    `/api/v1/index/verification/findings?${params.toString()}`,
    "verification findings request failed",
  );
}

export async function getAnalyticsOverview(nexusInstanceID?: number): Promise<AnalyticsOverview> {
  const params = new URLSearchParams();
  if (nexusInstanceID && nexusInstanceID > 0) {
    params.set("nexus_instance_id", String(nexusInstanceID));
  }
  return apiJSON<AnalyticsOverview>(
    `/api/v1/analytics/overview?${params.toString()}`,
    "analytics overview request failed",
  );
}

export async function listRepositoryAnalytics(
  options: AnalyticsListOptions = {},
): Promise<{ repositories: RepositoryAnalytics[] }> {
  return apiJSON<{ repositories: RepositoryAnalytics[] }>(
    `/api/v1/analytics/repositories?${analyticsParams(options).toString()}`,
    "repository analytics request failed",
  );
}

export async function listImageAnalytics(
  options: AnalyticsListOptions = {},
): Promise<{ images: ImageAnalytics[] }> {
  return apiJSON<{ images: ImageAnalytics[] }>(
    `/api/v1/analytics/images?${analyticsParams(options).toString()}`,
    "image analytics request failed",
  );
}

export async function listTagAnalytics(
  options: AnalyticsListOptions = {},
): Promise<{ tags: TagAnalytics[] }> {
  return apiJSON<{ tags: TagAnalytics[] }>(
    `/api/v1/analytics/tags?${analyticsParams(options).toString()}`,
    "tag analytics request failed",
  );
}

export async function listTrendSeries(
  options: AnalyticsListOptions = {},
): Promise<{ points: TrendPoint[] }> {
  return apiJSON<{ points: TrendPoint[] }>(
    `/api/v1/analytics/trends?${analyticsParams(options).toString()}`,
    "trend series request failed",
  );
}

export async function listTrendTopMovers(
  options: AnalyticsListOptions = {},
): Promise<{ movers: TrendTopMover[] }> {
  return apiJSON<{ movers: TrendTopMover[] }>(
    `/api/v1/analytics/trends/top-movers?${analyticsParams(options).toString()}`,
    "trend top movers request failed",
  );
}

export async function simulateCleanupPolicy(
  request: CleanupSimulationRequest,
): Promise<CleanupSimulationResult> {
  return apiJSON<CleanupSimulationResult>("/api/v1/cleanup/simulations", "cleanup simulation failed", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
  });
}

export async function listPackageFormatRepositories(
  options: AnalyticsListOptions = {},
): Promise<{ repositories: PackageFormatRepository[] }> {
  return apiJSON<{ repositories: PackageFormatRepository[] }>(
    `/api/v1/packages/repositories?${analyticsParams(options).toString()}`,
    "package repositories request failed",
  );
}

export async function requestCleanupExecution(
  request: CleanupExecutionRequest,
): Promise<CleanupExecution> {
  return apiJSON<CleanupExecution>("/api/v1/cleanup/executions", "cleanup execution failed", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
  });
}

export async function listCleanupExecutions(options: AnalyticsListOptions = {}): Promise<{ executions: CleanupExecution[] }> {
  const params = new URLSearchParams();
  if (options.nexusInstanceID && options.nexusInstanceID > 0) {
    params.set("nexus_instance_id", String(options.nexusInstanceID));
  }
  if (options.limit && options.limit > 0) {
    params.set("limit", String(options.limit));
  }
  return apiJSON<{ executions: CleanupExecution[] }>(
    `/api/v1/cleanup/executions?${params.toString()}`,
    "cleanup executions request failed",
  );
}

export async function listCleanupExecutionEvents(
  cleanupExecutionID: number,
  limit = 200,
): Promise<{ events: CleanupExecutionEvent[] }> {
  const params = new URLSearchParams({ limit: String(limit) });
  return apiJSON<{ events: CleanupExecutionEvent[] }>(
    `/api/v1/cleanup/executions/${cleanupExecutionID}/events?${params.toString()}`,
    "cleanup execution events request failed",
  );
}

export async function cancelCleanupExecution(cleanupExecutionID: number): Promise<CleanupExecution> {
  return apiJSON<CleanupExecution>(
    `/api/v1/cleanup/executions/${cleanupExecutionID}/cancel`,
    "cleanup execution cancel failed",
    { method: "POST" },
  );
}

export async function listTagPatterns(limit?: number): Promise<{ patterns: TagPattern[] }> {
  const params = new URLSearchParams();
  if (limit) {
    params.set("limit", String(limit));
  }
  const query = params.toString();
  return apiJSON<{ patterns: TagPattern[] }>(
    `/api/v1/admin/tag-patterns${query ? `?${query}` : ""}`,
    "tag patterns request failed",
  );
}

export async function upsertTagPattern(request: UpsertTagPatternRequest): Promise<TagPattern> {
  return apiJSON<TagPattern>("/api/v1/admin/tag-patterns", "save tag pattern failed", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
  });
}

export async function startTagReclassification(request: TagReclassificationRequest): Promise<IndexRun> {
  return apiJSON<IndexRun>("/api/v1/admin/tag-patterns/reclassify", "start tag reclassification failed", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
  });
}

export async function listTagPatternAnalytics(
  options: AnalyticsListOptions = {},
): Promise<{ patterns: TagPatternAnalytics[] }> {
  return apiJSON<{ patterns: TagPatternAnalytics[] }>(
    `/api/v1/analytics/tag-patterns?${analyticsParams(options).toString()}`,
    "tag pattern analytics request failed",
  );
}

export async function listTagPatternGrowth(
  options: AnalyticsListOptions = {},
): Promise<{ growth: TagPatternGrowth[] }> {
  return apiJSON<{ growth: TagPatternGrowth[] }>(
    `/api/v1/analytics/tag-patterns/growth?${analyticsParams(options).toString()}`,
    "tag pattern growth request failed",
  );
}

export async function listTagPatternExamples(
  options: AnalyticsListOptions = {},
): Promise<{ examples: TagPatternExample[] }> {
  return apiJSON<{ examples: TagPatternExample[] }>(
    `/api/v1/analytics/tag-patterns/examples?${analyticsParams(options).toString()}`,
    "tag pattern examples request failed",
  );
}

export async function listTagPatternSuggestions(
  options: AnalyticsListOptions = {},
): Promise<{ suggestions: TagPatternSuggestion[] }> {
  return apiJSON<{ suggestions: TagPatternSuggestion[] }>(
    `/api/v1/analytics/tag-patterns/suggestions?${analyticsParams(options).toString()}`,
    "tag pattern suggestions request failed",
  );
}

export async function dismissVerificationFinding(findingID: number): Promise<VerificationFinding> {
  return apiJSON<VerificationFinding>(
    `/api/v1/index/verification/findings/${findingID}/dismiss`,
    "dismiss verification finding failed",
    { method: "POST" },
  );
}

export async function pauseNexusIndexing(
  nexusInstanceID: number,
  request: PauseIndexingRequest,
): Promise<IndexingControl> {
  return apiJSON<IndexingControl>(`/api/v1/index/nexus/${nexusInstanceID}/pause`, "pause indexing failed", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
  });
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
  return apiJSON<IndexingControl>(
    `/api/v1/index/nexus/${nexusInstanceID}/resume`,
    "resume indexing failed",
    { method: "POST" },
  );
}

async function problemError(response: Response, fallback: string): Promise<APIError> {
  const body = (await response.json().catch(() => null)) as ProblemResponse | null;
  const message = body?.message || body?.detail || `${fallback} with status ${response.status}`;
  return new APIError(message, {
    status: response.status,
    code: body?.code,
    requestID: body?.request_id ?? response.headers.get("x-request-id") ?? undefined,
    retryable: body?.retryable,
  });
}
