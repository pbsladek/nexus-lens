import React, { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  cancelIndexRun,
  dismissVerificationFinding,
  getAnalyticsOverview,
  getHealth,
  getMe,
  listImageAnalytics,
  listIndexTasks,
  listIndexRuns,
  listIndexingSchedules,
  listNexusInstances,
  listRepositoryAnalytics,
  listTagAnalytics,
  listTagPatternAnalytics,
  listTagPatternExamples,
  listTagPatternGrowth,
  listTagPatternSuggestions,
  listTagPatterns,
  listVerificationFindings,
  login,
  logout,
  pauseNexusIndexing,
  resumeNexusIndexing,
  retryIndexTask,
  startTagReclassification,
  startIndexRun,
  updateNexusRateLimits,
  upsertIndexingSchedule,
  upsertTagPattern,
  type AnalyticsOverview,
  type AppUser,
  type ImageAnalytics,
  type IndexingSchedule,
  type IndexingControl,
  type IndexRun,
  type IndexTask,
  type NexusInstance,
  type RateLimitSettings,
  type RepositoryAnalytics,
  type StartIndexRunRequest,
  type TagAnalytics,
  type TagPattern,
  type TagPatternAnalytics,
  type TagPatternExample,
  type TagPatternGrowth,
  type TagPatternSuggestion,
  type UpsertIndexingScheduleRequest,
  type VerificationFinding,
} from "./api/client";
import "./styles.css";

type ScanType = StartIndexRunRequest["scan_type"];

function App() {
  const [apiStatus, setAPIStatus] = useState("checking");
  const [user, setUser] = useState<AppUser | null>(null);
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [authStatus, setAuthStatus] = useState("checking");
  const [runs, setRuns] = useState<IndexRun[]>([]);
  const [tasks, setTasks] = useState<IndexTask[]>([]);
  const [runsStatus, setRunsStatus] = useState("not loaded");
  const [taskRunFilter, setTaskRunFilter] = useState(0);
  const [scanType, setScanType] = useState<ScanType>("recent_sync");
  const [nexusName, setNexusName] = useState("local");
  const [baseURL, setBaseURL] = useState("");
  const [repositoryFilter, setRepositoryFilter] = useState("");
  const [windowDays, setWindowDays] = useState(7);
  const [pendingRunRequest, setPendingRunRequest] = useState<StartIndexRunRequest | null>(null);
  const [confirmationText, setConfirmationText] = useState("");
  const [runMessage, setRunMessage] = useState("");
  const [controlNexusID, setControlNexusID] = useState(1);
  const [pauseReason, setPauseReason] = useState("manual pause");
  const [control, setControl] = useState<IndexingControl | null>(null);
  const [findings, setFindings] = useState<VerificationFinding[]>([]);
  const [findingsStatus, setFindingsStatus] = useState("not loaded");
  const [instances, setInstances] = useState<NexusInstance[]>([]);
  const [schedules, setSchedules] = useState<IndexingSchedule[]>([]);
  const [settingsStatus, setSettingsStatus] = useState("not loaded");
  const [scheduleName, setScheduleName] = useState("recent-sync");
  const [scheduleScanType, setScheduleScanType] = useState<ScanType>("recent_sync");
  const [scheduleIntervalSeconds, setScheduleIntervalSeconds] = useState(1800);
  const [scheduleEnabled, setScheduleEnabled] = useState(true);
  const [scheduleDueNow, setScheduleDueNow] = useState(false);
  const [rateAssetListRPS, setRateAssetListRPS] = useState("4");
  const [rateRegistryManifestRPS, setRateRegistryManifestRPS] = useState("2");
  const [rateDefaultRPS, setRateDefaultRPS] = useState("8");
  const [analyticsStatus, setAnalyticsStatus] = useState("not loaded");
  const [overview, setOverview] = useState<AnalyticsOverview | null>(null);
  const [repositoryRows, setRepositoryRows] = useState<RepositoryAnalytics[]>([]);
  const [imageRows, setImageRows] = useState<ImageAnalytics[]>([]);
  const [tagRows, setTagRows] = useState<TagAnalytics[]>([]);
  const [analyticsNexusID, setAnalyticsNexusID] = useState(0);
  const [analyticsRepository, setAnalyticsRepository] = useState("");
  const [analyticsSearch, setAnalyticsSearch] = useState("");
  const [analyticsOlderThanDays, setAnalyticsOlderThanDays] = useState(30);
  const [analyticsNeverDownloaded, setAnalyticsNeverDownloaded] = useState(false);
  const [patternStatus, setPatternStatus] = useState("not loaded");
  const [patterns, setPatterns] = useState<TagPattern[]>([]);
  const [patternRows, setPatternRows] = useState<TagPatternAnalytics[]>([]);
  const [patternGrowth, setPatternGrowth] = useState<TagPatternGrowth[]>([]);
  const [patternExamples, setPatternExamples] = useState<TagPatternExample[]>([]);
  const [patternSuggestions, setPatternSuggestions] = useState<TagPatternSuggestion[]>([]);
  const [selectedPatternName, setSelectedPatternName] = useState("");
  const [patternGrowthWindowDays, setPatternGrowthWindowDays] = useState(30);
  const [patternName, setPatternName] = useState("custom_feature");
  const [patternDisplayName, setPatternDisplayName] = useState("Custom feature");
  const [patternRegex, setPatternRegex] = useState("^custom[-/_].+$");
  const [patternPriority, setPatternPriority] = useState(500);
  const [patternEnabled, setPatternEnabled] = useState(true);

  const isAdmin = user?.role === "admin";
  const isExclusiveRun = scanType === "bounded_reindex" || scanType === "full_reconcile";

  useEffect(() => {
    let active = true;
    getHealth()
      .then((health) => {
        if (active) {
          setAPIStatus(health.status);
        }
      })
      .catch(() => {
        if (active) {
          setAPIStatus("unreachable");
        }
      });
    getMe()
      .then((currentUser) => {
        if (active) {
          setUser(currentUser);
          setAuthStatus(currentUser ? "signed in" : "signed out");
        }
      })
      .catch(() => {
        if (active) {
          setAuthStatus("unreachable");
        }
      });
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    if (!user) {
      setRuns([]);
      setTasks([]);
      setFindings([]);
      setInstances([]);
      setSchedules([]);
      setOverview(null);
      setRepositoryRows([]);
      setImageRows([]);
      setTagRows([]);
      setPatterns([]);
      setPatternRows([]);
      setPatternGrowth([]);
      setPatternExamples([]);
      setPatternSuggestions([]);
      setRunsStatus("signed out");
      setFindingsStatus("signed out");
      setSettingsStatus("signed out");
      setAnalyticsStatus("signed out");
      setPatternStatus("signed out");
      return;
    }
    void refreshRuns();
    void refreshFindings();
    void refreshSettings();
    void refreshAnalytics();
    void refreshPatterns();
  }, [user]);

  async function refreshRuns() {
    setRunsStatus("loading");
    try {
      const response = await listIndexRuns();
      setRuns(response.runs ?? []);
      if ((response.runs ?? []).length > 0) {
        const firstRun = response.runs[0];
        setControlNexusID(firstRun.nexus_instance_id);
        setTaskRunFilter((current) => (current > 0 ? current : firstRun.id));
        await refreshTasks(firstRun.id);
      }
      setRunsStatus("loaded");
    } catch {
      setRunsStatus("failed");
    }
  }

  async function refreshTasks(scanRunID = taskRunFilter) {
    try {
      const response = await listIndexTasks(scanRunID > 0 ? scanRunID : undefined);
      setTasks(response.tasks ?? []);
    } catch {
      setTasks([]);
    }
  }

  async function refreshFindings() {
    setFindingsStatus("loading");
    try {
      const response = await listVerificationFindings({ status: "open" });
      setFindings(response.findings ?? []);
      setFindingsStatus("loaded");
    } catch {
      setFindings([]);
      setFindingsStatus("failed");
    }
  }

  async function refreshSettings() {
    setSettingsStatus("loading");
    try {
      const [instanceResponse, scheduleResponse] = await Promise.all([
        listNexusInstances(),
        listIndexingSchedules(controlNexusID > 0 ? controlNexusID : undefined),
      ]);
      setInstances(instanceResponse.instances ?? []);
      setSchedules(scheduleResponse.schedules ?? []);
      if ((instanceResponse.instances ?? []).length > 0) {
        const first = instanceResponse.instances[0];
        setControlNexusID((current) => (current > 0 ? current : first.id));
      setAnalyticsNexusID((current) => (current > 0 ? current : first.id));
        setNexusName((current) => current || first.name);
        setBaseURL((current) => current || first.base_url);
      }
      setSettingsStatus("loaded");
    } catch {
      setInstances([]);
      setSchedules([]);
      setSettingsStatus("failed");
    }
  }

  async function refreshPatterns() {
    setPatternStatus("loading");
    try {
      const options = {
        nexusInstanceID: analyticsNexusID > 0 ? analyticsNexusID : undefined,
        repositoryName: analyticsRepository.trim() || undefined,
        patternName: selectedPatternName || undefined,
        olderThanDays: analyticsOlderThanDays > 0 ? analyticsOlderThanDays : undefined,
        growthWindowDays: patternGrowthWindowDays > 0 ? patternGrowthWindowDays : undefined,
        neverDownloaded: analyticsNeverDownloaded,
        limit: 50,
      };
      const [definitionResponse, analyticsResponse, growthResponse, examplesResponse, suggestionsResponse] = await Promise.all([
        listTagPatterns(),
        listTagPatternAnalytics(options),
        listTagPatternGrowth(options),
        listTagPatternExamples(options),
        listTagPatternSuggestions(options),
      ]);
      setPatterns(definitionResponse.patterns ?? []);
      setPatternRows(analyticsResponse.patterns ?? []);
      setPatternGrowth(growthResponse.growth ?? []);
      setPatternExamples(examplesResponse.examples ?? []);
      setPatternSuggestions(suggestionsResponse.suggestions ?? []);
      setPatternStatus("loaded");
    } catch {
      setPatterns([]);
      setPatternRows([]);
      setPatternGrowth([]);
      setPatternExamples([]);
      setPatternSuggestions([]);
      setPatternStatus("failed");
    }
  }

  async function refreshAnalytics() {
    setAnalyticsStatus("loading");
    try {
      const options = {
        nexusInstanceID: analyticsNexusID > 0 ? analyticsNexusID : undefined,
        repositoryName: analyticsRepository.trim() || undefined,
        search: analyticsSearch.trim() || undefined,
        olderThanDays: analyticsOlderThanDays > 0 ? analyticsOlderThanDays : undefined,
        neverDownloaded: analyticsNeverDownloaded,
        limit: 50,
      };
      const [overviewResponse, repositoryResponse, imageResponse, tagResponse] = await Promise.all([
        getAnalyticsOverview(options.nexusInstanceID),
        listRepositoryAnalytics({
          nexusInstanceID: options.nexusInstanceID,
          search: options.search,
          limit: options.limit,
        }),
        listImageAnalytics(options),
        listTagAnalytics(options),
      ]);
      setOverview(overviewResponse);
      setRepositoryRows(repositoryResponse.repositories ?? []);
      setImageRows(imageResponse.images ?? []);
      setTagRows(tagResponse.tags ?? []);
      setAnalyticsStatus("loaded");
    } catch {
      setOverview(null);
      setRepositoryRows([]);
      setImageRows([]);
      setTagRows([]);
      setAnalyticsStatus("failed");
    }
  }

  async function handleLogin(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setAuthStatus("signing in");
    try {
      const session = await login({ username, password });
      setUser({ username: session.username, role: session.role });
      setPassword("");
      setAuthStatus("signed in");
    } catch {
      setUser(null);
      setAuthStatus("failed");
    }
  }

  async function handleLogout() {
    setAuthStatus("signing out");
    try {
      await logout();
    } finally {
      setUser(null);
      setAuthStatus("signed out");
    }
  }

  async function handleStartRun(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();

    const repositories = repositoryFilter
      .split(",")
      .map((value) => value.trim())
      .filter(Boolean);
    const request: StartIndexRunRequest = {
      ...(nexusName ? { nexus_name: nexusName } : {}),
      ...(baseURL ? { base_url: baseURL } : {}),
      scan_type: scanType,
      scope: {
        ...(repositories.length > 0 ? { repositories } : {}),
        ...(scanType === "bounded_reindex" ? { window_days: windowDays } : {}),
      },
    };

    if (isExclusiveRun) {
      setPendingRunRequest(request);
      setConfirmationText("");
      return;
    }
    await executeRun(request);
  }

  async function executeRun(request: StartIndexRunRequest) {
    setRunMessage("starting");
    try {
      const run = await startIndexRun(request);
      setRunMessage(`started ${run.scan_type} run ${run.id}`);
      setTaskRunFilter(run.id);
      setPendingRunRequest(null);
      setConfirmationText("");
      await refreshRuns();
    } catch (error) {
      setRunMessage(error instanceof Error ? error.message : "start failed");
    }
  }

  async function confirmPendingRun() {
    if (!pendingRunRequest) {
      return;
    }
    const requiredText = pendingRunRequest.nexus_name || "local";
    if (pendingRunRequest.scan_type === "full_reconcile" && confirmationText !== requiredText) {
      setRunMessage(`Type ${requiredText} to confirm the full reconcile.`);
      return;
    }
    await executeRun(pendingRunRequest);
  }

  async function handlePauseIndexing() {
    setRunMessage("pausing");
    try {
      const next = await pauseNexusIndexing(controlNexusID, { reason: pauseReason });
      setControl(next);
      setRunMessage(`paused Nexus ${next.nexus_instance_id}`);
    } catch (error) {
      setRunMessage(error instanceof Error ? error.message : "pause failed");
    }
  }

  async function handleResumeIndexing() {
    setRunMessage("resuming");
    try {
      const next = await resumeNexusIndexing(controlNexusID);
      setControl(next);
      setRunMessage(`resumed Nexus ${next.nexus_instance_id}`);
    } catch (error) {
      setRunMessage(error instanceof Error ? error.message : "resume failed");
    }
  }

  async function handleCancelRun(runID: number) {
    setRunMessage(`canceling run ${runID}`);
    try {
      await cancelIndexRun(runID);
      await refreshRuns();
      await refreshTasks(runID);
      setRunMessage(`canceled run ${runID}`);
    } catch (error) {
      setRunMessage(error instanceof Error ? error.message : "cancel failed");
    }
  }

  async function handleRetryTask(taskID: number) {
    setRunMessage(`retrying task ${taskID}`);
    try {
      await retryIndexTask(taskID);
      await refreshTasks();
      setRunMessage(`queued retry for task ${taskID}`);
    } catch (error) {
      setRunMessage(error instanceof Error ? error.message : "retry failed");
    }
  }

  async function handleDismissFinding(findingID: number) {
    setRunMessage(`dismissing finding ${findingID}`);
    try {
      await dismissVerificationFinding(findingID);
      await refreshFindings();
      setRunMessage(`dismissed finding ${findingID}`);
    } catch (error) {
      setRunMessage(error instanceof Error ? error.message : "dismiss failed");
    }
  }

  async function handleSaveSchedule(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const repositories = repositoryFilter
      .split(",")
      .map((value) => value.trim())
      .filter(Boolean);
    const request: UpsertIndexingScheduleRequest = {
      nexus_name: nexusName || "local",
      ...(baseURL ? { base_url: baseURL } : {}),
      name: scheduleName,
      scan_type: scheduleScanType,
      interval_seconds: scheduleIntervalSeconds,
      enabled: scheduleEnabled,
      due_now: scheduleDueNow,
      scope: {
        ...(repositories.length > 0 ? { repositories } : {}),
        ...(scheduleScanType === "bounded_reindex" ? { window_days: windowDays } : {}),
      },
    };
    setRunMessage("saving schedule");
    try {
      await upsertIndexingSchedule(request);
      await refreshSettings();
      setScheduleDueNow(false);
      setRunMessage(`saved schedule ${scheduleName}`);
    } catch (error) {
      setRunMessage(error instanceof Error ? error.message : "schedule save failed");
    }
  }

  async function handleSaveRateLimits(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const request: RateLimitSettings = {
      asset_list_rps: positiveNumberOrUndefined(rateAssetListRPS),
      registry_manifest_rps: positiveNumberOrUndefined(rateRegistryManifestRPS),
      default_rps: positiveNumberOrUndefined(rateDefaultRPS),
    };
    setRunMessage("saving rate limits");
    try {
      const next = await updateNexusRateLimits(controlNexusID, request);
      setControl(next);
      setRunMessage(`saved rate limits for Nexus ${controlNexusID}`);
    } catch (error) {
      setRunMessage(error instanceof Error ? error.message : "rate limit save failed");
    }
  }

  async function handleSaveTagPattern(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setRunMessage("saving tag pattern");
    try {
      await upsertTagPattern({
        name: patternName,
        display_name: patternDisplayName,
        description: "",
        tag_regex: patternRegex,
        priority: patternPriority,
        enabled: patternEnabled,
      });
      await refreshPatterns();
      setRunMessage(`saved tag pattern ${patternName}`);
    } catch (error) {
      setRunMessage(error instanceof Error ? error.message : "tag pattern save failed");
    }
  }

  async function handleStartTagReclassification() {
    if (analyticsNexusID <= 0) {
      setRunMessage("Nexus ID is required for tag reclassification.");
      return;
    }
    setRunMessage("starting tag reclassification");
    try {
      const run = await startTagReclassification({
        nexus_instance_id: analyticsNexusID,
        ...(analyticsRepository.trim() ? { repository_name: analyticsRepository.trim() } : {}),
      });
      setRunMessage(`started tag_reclassification run ${run.id}`);
      setTaskRunFilter(run.id);
      await refreshRuns();
    } catch (error) {
      setRunMessage(error instanceof Error ? error.message : "tag reclassification failed");
    }
  }

  return (
    <main className="shell">
      <header>
        <h1>Nexus Lens</h1>
        <div className="status-strip">
          <span>API: {apiStatus}</span>
          <span>Session: {authStatus}</span>
        </div>
      </header>
      {user ? (
        <section className="panel">
          <div>
            <h2>{user.username}</h2>
            <p>{user.role}</p>
          </div>
          <button type="button" onClick={handleLogout}>
            Sign out
          </button>
        </section>
      ) : (
        <section className="panel">
          <form className="login-form" onSubmit={handleLogin}>
            <label>
              Username
              <input
                autoComplete="username"
                value={username}
                onChange={(event) => setUsername(event.target.value)}
              />
            </label>
            <label>
              Password
              <input
                autoComplete="current-password"
                type="password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
            </label>
            <button type="submit">Sign in</button>
          </form>
        </section>
      )}
      <section className="panel stack">
        <div className="queue-header">
          <div>
            <h2>Artifact overview</h2>
            <p>{analyticsStatus}</p>
          </div>
          <label>
            Nexus ID
            <input
              min="0"
              type="number"
              value={analyticsNexusID}
              onChange={(event) => setAnalyticsNexusID(Number(event.target.value))}
            />
          </label>
          <button type="button" disabled={!user} onClick={refreshAnalytics}>
            Refresh
          </button>
        </div>
        <div className="metric-grid">
          <div className="metric">
            <span>Repositories</span>
            <strong>{overview?.repository_count ?? 0}</strong>
          </div>
          <div className="metric">
            <span>Images</span>
            <strong>{overview?.image_count ?? 0}</strong>
          </div>
          <div className="metric">
            <span>Active tags</span>
            <strong>{overview?.active_tag_count ?? 0}</strong>
          </div>
          <div className="metric">
            <span>Nexus asset bytes</span>
            <strong>{formatBytes(overview?.nexus_asset_bytes ?? 0)}</strong>
          </div>
          <div className="metric">
            <span>Docker logical bytes</span>
            <strong>{formatBytes(overview?.docker_logical_bytes ?? 0)}</strong>
          </div>
          <div className="metric">
            <span>Unique layer bytes</span>
            <strong>{formatBytes(overview?.docker_unique_layer_bytes ?? 0)}</strong>
          </div>
        </div>
        <div className="warning-row">
          <span>Missing digests: {overview?.tags_missing_digest_count ?? 0}</span>
          <span>Missing manifests: {overview?.tags_missing_manifest_count ?? 0}</span>
          <span>Last successful run: {formatDate(overview?.last_successful_run_at)}</span>
        </div>
      </section>
      <section className="panel stack">
        <div className="queue-header">
          <div>
            <h2>Artifact explorer</h2>
            <p>Storage values are API-derived estimates from Nexus assets and Docker manifests.</p>
          </div>
          <label>
            Older than days
            <input
              min="0"
              type="number"
              value={analyticsOlderThanDays}
              onChange={(event) => setAnalyticsOlderThanDays(Number(event.target.value))}
            />
          </label>
          <button
            type="button"
            disabled={!user}
            onClick={() => {
              void refreshAnalytics();
              void refreshPatterns();
            }}
          >
            Apply
          </button>
        </div>
        <div className="explorer-filters">
          <label>
            Repository
            <input
              placeholder="docker-hosted-a"
              value={analyticsRepository}
              onChange={(event) => setAnalyticsRepository(event.target.value)}
            />
          </label>
          <label>
            Search image or tag
            <input
              placeholder="feature, latest, sha"
              value={analyticsSearch}
              onChange={(event) => setAnalyticsSearch(event.target.value)}
            />
          </label>
          <label className="inline-check">
            <input
              checked={analyticsNeverDownloaded}
              type="checkbox"
              onChange={(event) => setAnalyticsNeverDownloaded(event.target.checked)}
            />
            Never downloaded
          </label>
        </div>
        <h3>Repositories</h3>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Repository</th>
                <th>Images</th>
                <th>Active tags</th>
                <th>Nexus assets</th>
                <th>Docker logical</th>
                <th>Unique layers</th>
                <th>Last seen</th>
              </tr>
            </thead>
            <tbody>
              {repositoryRows.length === 0 ? (
                <tr>
                  <td colSpan={7}>No repositories loaded.</td>
                </tr>
              ) : (
                repositoryRows.map((repository) => (
                  <tr key={repository.repository_id}>
                    <td>{repository.repository_name}</td>
                    <td>{repository.image_count}</td>
                    <td>{repository.active_tag_count}</td>
                    <td>{formatBytes(repository.nexus_asset_bytes)}</td>
                    <td>{formatBytes(repository.docker_logical_bytes)}</td>
                    <td>{formatBytes(repository.docker_unique_layer_bytes)}</td>
                    <td>{formatDate(repository.last_seen_at)}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
        <h3>Images</h3>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Repository</th>
                <th>Image</th>
                <th>Active tags</th>
                <th>Docker logical</th>
                <th>Unique layers</th>
                <th>Oldest tag</th>
                <th>Newest seen</th>
              </tr>
            </thead>
            <tbody>
              {imageRows.length === 0 ? (
                <tr>
                  <td colSpan={7}>No images loaded.</td>
                </tr>
              ) : (
                imageRows.map((image) => (
                  <tr key={image.image_id}>
                    <td>{image.repository_name}</td>
                    <td>{image.image_name}</td>
                    <td>{image.active_tag_count}</td>
                    <td>{formatBytes(image.docker_logical_bytes)}</td>
                    <td>{formatBytes(image.docker_unique_layer_bytes)}</td>
                    <td>{formatDate(image.oldest_tag_first_seen_at)}</td>
                    <td>{formatDate(image.newest_tag_last_seen_at)}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
        <h3>Tags</h3>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Repository</th>
                <th>Image:tag</th>
                <th>First seen</th>
                <th>Last seen</th>
                <th>Last pulled</th>
                <th>Docker logical</th>
                <th>Reclaimable</th>
                <th>Shared layers</th>
              </tr>
            </thead>
            <tbody>
              {tagRows.length === 0 ? (
                <tr>
                  <td colSpan={8}>No tags loaded.</td>
                </tr>
              ) : (
                tagRows.map((tag) => (
                  <tr key={tag.tag_id}>
                    <td>{tag.repository_name}</td>
                    <td>{tag.image_name}:{tag.tag}</td>
                    <td>{formatDate(tag.first_seen_at)}</td>
                    <td>{formatDate(tag.last_seen_at)}</td>
                    <td>{tag.last_downloaded_at ? formatDate(tag.last_downloaded_at) : "never"}</td>
                    <td>{formatBytes(tag.docker_logical_bytes)}</td>
                    <td>{formatBytes(tag.estimated_reclaimable_bytes)}</td>
                    <td>{formatBytes(tag.shared_layer_bytes)}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </section>
      <section className="panel stack">
        <div className="queue-header">
          <div>
            <h2>Tag patterns</h2>
            <p>{patternStatus}</p>
          </div>
          <label>
            Pattern
            <select
              value={selectedPatternName}
              onChange={(event) => setSelectedPatternName(event.target.value)}
            >
              <option value="">All patterns</option>
              {patterns.map((pattern) => (
                <option key={pattern.id} value={pattern.name}>
                  {pattern.display_name}
                </option>
              ))}
              <option value="unclassified">Unclassified</option>
            </select>
          </label>
          <label>
            Growth window days
            <input
              min="1"
              type="number"
              value={patternGrowthWindowDays}
              onChange={(event) => setPatternGrowthWindowDays(Number(event.target.value))}
            />
          </label>
          <button type="button" disabled={!user} onClick={refreshPatterns}>
            Refresh
          </button>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Pattern</th>
                <th>Active tags</th>
                <th>Never pulled</th>
                <th>Docker logical</th>
                <th>Oldest tag</th>
              </tr>
            </thead>
            <tbody>
              {patternRows.length === 0 ? (
                <tr>
                  <td colSpan={5}>No pattern analytics loaded.</td>
                </tr>
              ) : (
                patternRows.map((pattern) => (
                  <tr key={pattern.pattern_name}>
                    <td>{pattern.pattern_name}</td>
                    <td>{pattern.active_tag_count}</td>
                    <td>{pattern.never_downloaded_count}</td>
                    <td>{formatBytes(pattern.docker_logical_bytes)}</td>
                    <td>{formatDate(pattern.oldest_tag_first_seen_at)}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
        <h3>Growth</h3>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Repository</th>
                <th>Namespace</th>
                <th>Pattern</th>
                <th>Active tags</th>
                <th>Growth tags</th>
                <th>Growth bytes</th>
                <th>Total bytes</th>
                <th>Never pulled</th>
              </tr>
            </thead>
            <tbody>
              {patternGrowth.length === 0 ? (
                <tr>
                  <td colSpan={8}>No growth rows loaded.</td>
                </tr>
              ) : (
                patternGrowth.map((growth) => (
                  <tr key={`${growth.repository_name}/${growth.image_namespace}/${growth.pattern_name}`}>
                    <td>{growth.repository_name}</td>
                    <td>{growth.image_namespace}</td>
                    <td>{growth.pattern_name}</td>
                    <td>{growth.active_tag_count}</td>
                    <td>{growth.growth_tag_count}</td>
                    <td>{formatBytes(growth.growth_logical_bytes)}</td>
                    <td>{formatBytes(growth.docker_logical_bytes)}</td>
                    <td>{growth.never_downloaded_count}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
        <h3>Examples</h3>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Pattern</th>
                <th>Repository</th>
                <th>Image:tag</th>
                <th>Last seen</th>
              </tr>
            </thead>
            <tbody>
              {patternExamples.length === 0 ? (
                <tr>
                  <td colSpan={4}>No examples loaded.</td>
                </tr>
              ) : (
                patternExamples.map((example) => (
                  <tr key={`${example.repository_name}/${example.image_name}:${example.tag}`}>
                    <td>{example.pattern_name}</td>
                    <td>{example.repository_name}</td>
                    <td>{example.image_name}:{example.tag}</td>
                    <td>{formatDate(example.last_seen_at)}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
        <h3>Definitions</h3>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Priority</th>
                <th>Enabled</th>
                <th>Built in</th>
                <th>Regex</th>
              </tr>
            </thead>
            <tbody>
              {patterns.length === 0 ? (
                <tr>
                  <td colSpan={5}>No pattern definitions loaded.</td>
                </tr>
              ) : (
                patterns.map((pattern) => (
                  <tr key={pattern.id}>
                    <td>{pattern.display_name}</td>
                    <td>{pattern.priority}</td>
                    <td>{pattern.enabled ? "yes" : "no"}</td>
                    <td>{pattern.built_in ? "yes" : "no"}</td>
                    <td>{pattern.tag_regex}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
        <h3>Suggestions</h3>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Shape</th>
                <th>Tags</th>
                <th>Examples</th>
                <th>Suggested regex</th>
              </tr>
            </thead>
            <tbody>
              {patternSuggestions.length === 0 ? (
                <tr>
                  <td colSpan={4}>No unclassified suggestions.</td>
                </tr>
              ) : (
                patternSuggestions.map((suggestion) => (
                  <tr key={suggestion.tag_shape}>
                    <td>{suggestion.tag_shape}</td>
                    <td>{suggestion.tag_count}</td>
                    <td>{suggestion.example_tags.join(", ")}</td>
                    <td>{suggestion.suggested_regex}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
        <form className="settings-form" onSubmit={handleSaveTagPattern}>
          <label>
            Pattern name
            <input value={patternName} onChange={(event) => setPatternName(event.target.value)} />
          </label>
          <label>
            Display name
            <input
              value={patternDisplayName}
              onChange={(event) => setPatternDisplayName(event.target.value)}
            />
          </label>
          <label>
            Regex
            <input value={patternRegex} onChange={(event) => setPatternRegex(event.target.value)} />
          </label>
          <label>
            Priority
            <input
              min="1"
              type="number"
              value={patternPriority}
              onChange={(event) => setPatternPriority(Number(event.target.value))}
            />
          </label>
          <label className="inline-check">
            <input
              checked={patternEnabled}
              type="checkbox"
              onChange={(event) => setPatternEnabled(event.target.checked)}
            />
            Enabled
          </label>
          <button type="submit" disabled={!isAdmin}>
            Save pattern
          </button>
        </form>
        <div className="control-form">
          <button type="button" disabled={!isAdmin || analyticsNexusID <= 0} onClick={handleStartTagReclassification}>
            Reclassify
          </button>
          <p className="run-message">
            Reclassification uses the selected Nexus ID and repository filter from Artifact explorer.
          </p>
        </div>
      </section>
      <section className="panel">
        <div>
          <h2>Index operations</h2>
          <p>{runsStatus}</p>
        </div>
        <button type="button" onClick={refreshRuns} disabled={!user}>
          Refresh
        </button>
      </section>
      {user ? (
        <section className="panel stack">
          <form className="run-form" onSubmit={handleStartRun}>
            <label>
              Nexus name
              <input value={nexusName} onChange={(event) => setNexusName(event.target.value)} />
            </label>
            <label>
              Base URL
              <input
                placeholder="default from API"
                value={baseURL}
                onChange={(event) => setBaseURL(event.target.value)}
              />
            </label>
            <label>
              Run type
              <select
                value={scanType}
                onChange={(event) => {
                  setScanType(event.target.value as ScanType);
                }}
              >
                <option value="recent_sync">Recent sync</option>
                <option value="bounded_reindex">Bounded reindex</option>
                <option value="full_reconcile">Full reconcile</option>
                <option value="verification">Verification</option>
                <option value="trend_snapshot">Trend snapshot</option>
              </select>
            </label>
            <label>
              Repositories
              <input
                placeholder="docker-hosted-a, docker-hosted-b"
                value={repositoryFilter}
                onChange={(event) => setRepositoryFilter(event.target.value)}
              />
            </label>
            {scanType === "bounded_reindex" ? (
              <label>
                Window days
                <input
                  min="1"
                  type="number"
                  value={windowDays}
                  onChange={(event) => setWindowDays(Number(event.target.value))}
                />
              </label>
            ) : null}
            {isExclusiveRun ? (
              <p className="confirmation">
                This operation will block other indexing for this Nexus until it finishes.
              </p>
            ) : null}
            <button type="submit" disabled={!isAdmin}>
              Start
            </button>
          </form>
          <div className="control-form">
            <label>
              Nexus ID
              <input
                min="1"
                type="number"
                value={controlNexusID}
                onChange={(event) => setControlNexusID(Number(event.target.value))}
              />
            </label>
            <label>
              Pause reason
              <input value={pauseReason} onChange={(event) => setPauseReason(event.target.value)} />
            </label>
            <button type="button" disabled={!isAdmin} onClick={handlePauseIndexing}>
              Pause
            </button>
            <button type="button" disabled={!isAdmin} onClick={handleResumeIndexing}>
              Resume
            </button>
          </div>
          {control ? (
            <p className="run-message">
              Nexus {control.nexus_instance_id}: {control.paused ? "paused" : "active"}
              {control.circuit_open_until ? `, circuit open until ${new Date(control.circuit_open_until).toLocaleString()}` : ""}
            </p>
          ) : null}
          {runMessage ? <p className="run-message">{runMessage}</p> : null}
        </section>
      ) : null}
      <section className="panel stack">
        <h2>Recent runs</h2>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>ID</th>
                <th>Type</th>
                <th>Status</th>
                <th>Tasks</th>
                <th>Started</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {runs.length === 0 ? (
                <tr>
                  <td colSpan={6}>No runs loaded.</td>
                </tr>
              ) : (
                runs.map((run) => (
                  <tr key={run.id}>
                    <td>
                      <button
                        className="link-button"
                        type="button"
                        onClick={() => {
                          setTaskRunFilter(run.id);
                          void refreshTasks(run.id);
                        }}
                      >
                        {run.id}
                      </button>
                    </td>
                    <td>{run.scan_type}</td>
                    <td>{run.status}</td>
                    <td>
                      {run.completed_tasks}/{run.failed_tasks}
                    </td>
                    <td>{new Date(run.started_at).toLocaleString()}</td>
                    <td>
                      <button
                        type="button"
                        disabled={!isAdmin || run.status !== "running"}
                        onClick={() => handleCancelRun(run.id)}
                      >
                        Cancel
                      </button>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </section>
      <section className="panel stack">
        <div className="queue-header">
          <h2>Queue</h2>
          <label>
            Run ID
            <input
              min="0"
              type="number"
              value={taskRunFilter}
              onChange={(event) => setTaskRunFilter(Number(event.target.value))}
            />
          </label>
          <button type="button" disabled={!user} onClick={() => refreshTasks()}>
            Refresh
          </button>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>ID</th>
                <th>Type</th>
                <th>Status</th>
                <th>Attempt</th>
                <th>Available</th>
                <th>Error</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {tasks.length === 0 ? (
                <tr>
                  <td colSpan={7}>No tasks loaded.</td>
                </tr>
              ) : (
                tasks.map((task) => (
                  <tr key={task.id}>
                    <td>{task.id}</td>
                    <td>{task.task_type}</td>
                    <td>{task.status}</td>
                    <td>
                      {task.attempt}/{task.max_attempts}
                    </td>
                    <td>{new Date(task.available_at).toLocaleString()}</td>
                    <td title={task.error_message}>{task.error_code || task.error_class}</td>
                    <td>
                      <button
                        type="button"
                        disabled={!isAdmin || (task.status !== "failed" && task.status !== "retryable")}
                        onClick={() => handleRetryTask(task.id)}
                      >
                        Retry
                      </button>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </section>
      <section className="panel stack">
        <div className="queue-header">
          <div>
            <h2>Admin settings</h2>
            <p>{settingsStatus}</p>
          </div>
          <button type="button" disabled={!user} onClick={refreshSettings}>
            Refresh
          </button>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>ID</th>
                <th>Nexus</th>
                <th>Base URL</th>
                <th>Updated</th>
              </tr>
            </thead>
            <tbody>
              {instances.length === 0 ? (
                <tr>
                  <td colSpan={4}>No Nexus instances loaded.</td>
                </tr>
              ) : (
                instances.map((instance) => (
                  <tr key={instance.id}>
                    <td>{instance.id}</td>
                    <td>{instance.name}</td>
                    <td>{instance.base_url}</td>
                    <td>{new Date(instance.updated_at).toLocaleString()}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
        <form className="settings-form" onSubmit={handleSaveSchedule}>
          <label>
            Schedule name
            <input value={scheduleName} onChange={(event) => setScheduleName(event.target.value)} />
          </label>
          <label>
            Schedule type
            <select
              value={scheduleScanType}
              onChange={(event) => setScheduleScanType(event.target.value as ScanType)}
            >
              <option value="recent_sync">Recent sync</option>
              <option value="bounded_reindex">Bounded reindex</option>
              <option value="full_reconcile">Full reconcile</option>
              <option value="verification">Verification</option>
              <option value="trend_snapshot">Trend snapshot</option>
            </select>
          </label>
          <label>
            Interval seconds
            <input
              min="60"
              type="number"
              value={scheduleIntervalSeconds}
              onChange={(event) => setScheduleIntervalSeconds(Number(event.target.value))}
            />
          </label>
          <label className="inline-check">
            <input
              checked={scheduleEnabled}
              type="checkbox"
              onChange={(event) => setScheduleEnabled(event.target.checked)}
            />
            Enabled
          </label>
          <label className="inline-check">
            <input
              checked={scheduleDueNow}
              type="checkbox"
              onChange={(event) => setScheduleDueNow(event.target.checked)}
            />
            Run when due
          </label>
          <button type="submit" disabled={!isAdmin}>
            Save schedule
          </button>
        </form>
        <form className="settings-form" onSubmit={handleSaveRateLimits}>
          <label>
            Asset list RPS
            <input value={rateAssetListRPS} onChange={(event) => setRateAssetListRPS(event.target.value)} />
          </label>
          <label>
            Manifest RPS
            <input
              value={rateRegistryManifestRPS}
              onChange={(event) => setRateRegistryManifestRPS(event.target.value)}
            />
          </label>
          <label>
            Default RPS
            <input value={rateDefaultRPS} onChange={(event) => setRateDefaultRPS(event.target.value)} />
          </label>
          <button type="submit" disabled={!isAdmin || controlNexusID <= 0}>
            Save rate limits
          </button>
        </form>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Type</th>
                <th>Enabled</th>
                <th>Interval</th>
                <th>Next run</th>
              </tr>
            </thead>
            <tbody>
              {schedules.length === 0 ? (
                <tr>
                  <td colSpan={5}>No schedules loaded.</td>
                </tr>
              ) : (
                schedules.map((schedule) => (
                  <tr key={schedule.id}>
                    <td>{schedule.name}</td>
                    <td>{schedule.scan_type}</td>
                    <td>{schedule.enabled ? "yes" : "no"}</td>
                    <td>{schedule.interval_seconds}s</td>
                    <td>{schedule.next_run_at ? new Date(schedule.next_run_at).toLocaleString() : ""}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </section>
      <section className="panel stack">
        <div className="queue-header">
          <div>
            <h2>Verification findings</h2>
            <p>{findingsStatus}</p>
          </div>
          <button type="button" disabled={!user} onClick={refreshFindings}>
            Refresh
          </button>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>ID</th>
                <th>Severity</th>
                <th>Type</th>
                <th>Repository</th>
                <th>Subject</th>
                <th>Last seen</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {findings.length === 0 ? (
                <tr>
                  <td colSpan={7}>No open findings.</td>
                </tr>
              ) : (
                findings.map((finding) => (
                  <tr key={finding.id}>
                    <td>{finding.id}</td>
                    <td>{finding.severity}</td>
                    <td title={finding.message}>{finding.finding_type}</td>
                    <td>{finding.repository_name}</td>
                    <td>{finding.subject}</td>
                    <td>{new Date(finding.last_seen_at).toLocaleString()}</td>
                    <td>
                      <button
                        type="button"
                        disabled={!isAdmin}
                        onClick={() => handleDismissFinding(finding.id)}
                      >
                        Dismiss
                      </button>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </section>
      {pendingRunRequest ? (
        <div className="modal-backdrop" role="presentation">
          <section className="modal" role="dialog" aria-modal="true" aria-labelledby="confirm-title">
            <h2 id="confirm-title">Confirm index operation</h2>
            <p>
              {pendingRunRequest.scan_type === "full_reconcile"
                ? "Full reconcile walks the selected hosted Docker repositories, confirms missing tags as deleted for completed scopes, and blocks other indexing for this Nexus while it runs."
                : "Bounded reindex refreshes only the selected repository and time window. It improves freshness but does not prove deletion outside that scope."}
            </p>
            {pendingRunRequest.scan_type === "full_reconcile" ? (
              <label>
                Type {pendingRunRequest.nexus_name || "local"} to confirm
                <input
                  autoFocus
                  value={confirmationText}
                  onChange={(event) => setConfirmationText(event.target.value)}
                />
              </label>
            ) : null}
            <div className="modal-actions">
              <button type="button" onClick={() => setPendingRunRequest(null)}>
                Cancel
              </button>
              <button
                type="button"
                disabled={
                  pendingRunRequest.scan_type === "full_reconcile" &&
                  confirmationText !== (pendingRunRequest.nexus_name || "local")
                }
                onClick={confirmPendingRun}
              >
                Start
              </button>
            </div>
          </section>
        </div>
      ) : null}
    </main>
  );
}

function positiveNumberOrUndefined(value: string): number | undefined {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : undefined;
}

function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) {
    return "0 B";
  }
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  let next = value;
  let unit = 0;
  while (next >= 1024 && unit < units.length - 1) {
    next /= 1024;
    unit += 1;
  }
  return `${next.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
}

function formatDate(value?: string): string {
  return value ? new Date(value).toLocaleString() : "";
}

createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
);
