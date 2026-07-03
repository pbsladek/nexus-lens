import { expect, test } from "@playwright/test";

const now = "2026-06-28T04:00:00Z";

test.beforeEach(async ({ page }) => {
  await page.route("http://127.0.0.1:5601/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;

    if (path === "/api/v1/health") {
      await route.fulfill({ json: { status: "ok" } });
      return;
    }
    if (path === "/api/v1/me") {
      await route.fulfill({
        json: {
          username: "admin",
          role: "admin",
          capabilities: [
            "read",
            "manage_index",
            "manage_settings",
            "manage_tag_patterns",
            "manage_findings",
            "execute_cleanup",
          ],
        },
      });
      return;
    }
    if (path === "/api/v1/admin/status") {
      await route.fulfill({ json: { status: "ok" } });
      return;
    }
    if (path === "/api/v1/index/runs" && request.method() === "GET") {
      await route.fulfill({
        json: {
          runs: [
            {
              id: 42,
              nexus_instance_id: 7,
              scan_type: "recent_sync",
              status: "running",
              completed_tasks: 1,
              failed_tasks: 0,
              started_at: now,
            },
          ],
        },
      });
      return;
    }
    if (path === "/api/v1/index/tasks") {
      await route.fulfill({
        json: {
          tasks: [
            {
              id: 99,
              scan_run_id: 42,
              nexus_instance_id: 7,
              task_type: "repository_discovery",
              task_key: "repository-discovery",
              cost_class: "asset_list",
              status: "failed",
              attempt: 3,
              max_attempts: 3,
              available_at: now,
              error_code: "nexus_repository_list_network_failed",
            },
          ],
        },
      });
      return;
    }
    if (path === "/api/v1/index/verification/findings") {
      await route.fulfill({
        json: {
          findings: [
            {
              id: 17,
              nexus_instance_id: 7,
              severity: "high",
              finding_type: "docker_tag_digest_missing",
              repository_name: "docker-hosted-a",
              subject: "image:tag",
              status: "open",
              created_at: now,
              last_seen_at: now,
            },
          ],
        },
      });
      return;
    }
    if (path === "/api/v1/analytics/overview") {
      await route.fulfill({
        json: {
          repository_count: 2,
          image_count: 4,
          active_tag_count: 12,
          tags_missing_digest_count: 1,
          tags_missing_manifest_count: 2,
          nexus_asset_bytes: 3221225472,
          docker_logical_bytes: 5368709120,
          docker_unique_layer_bytes: 2147483648,
          last_successful_run_at: now,
        },
      });
      return;
    }
    if (path === "/api/v1/analytics/repositories") {
      await route.fulfill({
        json: {
          repositories: [
            {
              repository_id: 1,
              nexus_instance_id: 7,
              nexus_name: "local",
              repository_name: "docker-hosted-a",
              image_count: 2,
              active_tag_count: 8,
              nexus_asset_bytes: 2147483648,
              docker_logical_bytes: 3221225472,
              docker_unique_layer_bytes: 1610612736,
              last_seen_at: now,
            },
          ],
        },
      });
      return;
    }
    if (path === "/api/v1/analytics/images") {
      await route.fulfill({
        json: {
          images: [
            {
              image_id: 11,
              repository_id: 1,
              nexus_instance_id: 7,
              nexus_name: "local",
              repository_name: "docker-hosted-a",
              image_name: "demo/app",
              active_tag_count: 5,
              docker_logical_bytes: 2684354560,
              docker_unique_layer_bytes: 1073741824,
              oldest_tag_first_seen_at: "2026-05-01T00:00:00Z",
              newest_tag_last_seen_at: now,
            },
          ],
        },
      });
      return;
    }
    if (path === "/api/v1/analytics/tags") {
      await route.fulfill({
        json: {
          tags: [
            {
              tag_id: 101,
              image_id: 11,
              repository_id: 1,
              nexus_instance_id: 7,
              nexus_name: "local",
              repository_name: "docker-hosted-a",
              image_name: "demo/app",
              tag: "feature-123",
              digest: "sha256:abc",
              media_type: "application/vnd.oci.image.manifest.v1+json",
              nexus_tag_size_bytes: 1024,
              docker_logical_bytes: 2684354560,
              docker_layer_bytes: 2147483648,
              estimated_reclaimable_bytes: 1073741824,
              shared_layer_bytes: 1073741824,
              first_seen_at: "2026-05-01T00:00:00Z",
              last_seen_at: now,
            },
          ],
        },
      });
      return;
    }
    if (path === "/api/v1/analytics/trends") {
      await route.fulfill({
        json: {
          points: [
            {
              snapshot_date: "2026-06-26",
              captured_at: "2026-06-26T04:00:00Z",
              metric_value: 3221225472,
              missing: false,
            },
            {
              snapshot_date: "2026-06-27",
              missing: true,
            },
            {
              snapshot_date: "2026-06-28",
              captured_at: now,
              metric_value: 5368709120,
              missing: false,
            },
          ],
        },
      });
      return;
    }
    if (path === "/api/v1/packages/repositories") {
      await route.fulfill({
        json: {
          repositories: [
            {
              repository_id: 1,
              nexus_instance_id: 7,
              nexus_name: "local",
              repository_name: "docker-hosted-a",
              format: "docker",
              type: "hosted",
              support_status: "indexed",
              support_reason: "docker/oci analytics are fully indexed",
              asset_count: 49,
              asset_bytes: 2147483648,
              last_seen_at: now,
            },
            {
              repository_id: 21,
              nexus_instance_id: 7,
              nexus_name: "local",
              repository_name: "pypi-hosted",
              format: "pypi",
              type: "hosted",
              support_status: "asset_indexed",
              support_reason: "hosted package repository discovered; format-specific artifact indexing is not enabled yet",
              asset_count: 0,
              asset_bytes: 0,
              last_seen_at: now,
            },
          ],
        },
      });
      return;
    }
    if (path === "/api/v1/analytics/trends/top-movers") {
      await route.fulfill({
        json: {
          movers: [
            {
              repository_id: 1,
              repository_name: "docker-hosted-a",
              image_id: 11,
              image_name: "demo/app",
              start_value: 3221225472,
              end_value: 5368709120,
              delta_value: 2147483648,
              delta_percent: 66.7,
            },
          ],
        },
      });
      return;
    }
    if (path === "/api/v1/analytics/tag-patterns") {
      await route.fulfill({
        json: {
          patterns: [
            {
              pattern_name: "semver",
              active_tag_count: 2,
              docker_logical_bytes: 2147483648,
              oldest_tag_first_seen_at: "2026-05-01T00:00:00Z",
              never_downloaded_count: 1,
            },
            {
              pattern_name: "feature_branch",
              active_tag_count: 1,
              docker_logical_bytes: 1073741824,
              oldest_tag_first_seen_at: "2026-05-03T00:00:00Z",
              never_downloaded_count: 0,
            },
          ],
        },
      });
      return;
    }
    if (path === "/api/v1/analytics/tag-patterns/growth") {
      await route.fulfill({
        json: {
          growth: [
            {
              repository_name: "docker-hosted-a",
              image_namespace: "demo",
              pattern_name: "feature_branch",
              active_tag_count: 3,
              docker_logical_bytes: 3221225472,
              growth_tag_count: 2,
              growth_logical_bytes: 2147483648,
              never_downloaded_count: 1,
              oldest_tag_first_seen_at: "2026-05-01T00:00:00Z",
            },
          ],
        },
      });
      return;
    }
    if (path === "/api/v1/analytics/tag-patterns/examples") {
      await route.fulfill({
        json: {
          examples: [
            {
              pattern_name: "semver",
              repository_name: "docker-hosted-a",
              image_name: "demo/app",
              tag: "v1.2.3",
              first_seen_at: "2026-05-01T00:00:00Z",
              last_seen_at: now,
            },
          ],
        },
      });
      return;
    }
    if (path === "/api/v1/analytics/tag-patterns/suggestions") {
      await route.fulfill({
        json: {
          suggestions: [
            {
              tag_shape: "A-0-A",
              tag_count: 3,
              example_tags: ["team-123-build"],
              suggested_regex: "^[A-Za-z]+-[0-9]+-[A-Za-z]+$",
            },
          ],
        },
      });
      return;
    }
    if (path === "/api/v1/admin/tag-patterns" && request.method() === "GET") {
      await route.fulfill({
        json: {
          patterns: [
            {
              id: 1,
              name: "semver",
              display_name: "SemVer",
              description: "Semantic version tags",
              tag_regex: "^v?[0-9]+[.][0-9]+[.][0-9]+$",
              priority: 10,
              enabled: true,
              built_in: true,
              created_at: now,
              updated_at: now,
            },
            {
              id: 2,
              name: "feature_branch",
              display_name: "Feature branch",
              description: "Feature branch tags",
              tag_regex: "^(feat|feature)[-/_].+$",
              priority: 70,
              enabled: true,
              built_in: true,
              created_at: now,
              updated_at: now,
            },
          ],
        },
      });
      return;
    }
    if (path === "/api/v1/admin/nexus-instances") {
      await route.fulfill({
        json: {
          instances: [
            {
              id: 7,
              name: "local",
              base_url: "http://nexus:8081",
              created_at: now,
              updated_at: now,
            },
          ],
        },
      });
      return;
    }
    if (path === "/api/v1/admin/indexing-schedules" && request.method() === "GET") {
      await route.fulfill({
        json: {
          schedules: [
            {
              id: 5,
              nexus_instance_id: 7,
              nexus_name: "local",
              nexus_base_url: "http://nexus:8081",
              scan_type: "recent_sync",
              name: "recent-sync",
              enabled: true,
              schedule_expr: "every 1800s",
              interval_seconds: 1800,
              timezone: "UTC",
              scope: {},
              lock_conflict_behavior: "skip_blocked",
              created_at: now,
              updated_at: now,
            },
          ],
        },
      });
      return;
    }
    if (path === "/api/v1/admin/retention-settings" && request.method() === "GET") {
      await route.fulfill({
        json: {
          interval_seconds: 3600,
          scan_run_days: 30,
          cleanup_execution_days: 365,
          verification_finding_days: 180,
          trend_snapshot_days: 365,
          configured: true,
          updated_by: "admin",
          updated_at: now,
        },
      });
      return;
    }
    if (path === "/api/v1/admin/retention-settings" && request.method() === "PUT") {
      await route.fulfill({
        json: {
          ...request.postDataJSON(),
          configured: true,
          updated_by: "admin",
          updated_at: now,
        },
      });
      return;
    }
    if (path === "/api/v1/index/runs" && request.method() === "POST") {
      await route.fulfill({
        status: 202,
        json: {
          id: 77,
          nexus_instance_id: 7,
          scan_type: "full_reconcile",
          status: "running",
          completed_tasks: 0,
          failed_tasks: 0,
          started_at: now,
        },
      });
      return;
    }
    if (path.endsWith("/pause")) {
      await route.fulfill({
        json: {
          nexus_instance_id: 7,
          paused: true,
          paused_reason: "manual pause",
          paused_by: "admin",
          circuit_failure_count: 0,
        },
      });
      return;
    }
    if (path.endsWith("/resume")) {
      await route.fulfill({
        json: {
          nexus_instance_id: 7,
          paused: false,
          circuit_failure_count: 0,
        },
      });
      return;
    }
    if (path.endsWith("/retry") || path.endsWith("/cancel") || path.endsWith("/dismiss")) {
      await route.fulfill({ json: {} });
      return;
    }
    if (path.endsWith("/rate-limits")) {
      await route.fulfill({
        json: {
          nexus_instance_id: 7,
          paused: false,
          circuit_failure_count: 0,
          asset_list_rps: 3,
          registry_manifest_rps: 1.5,
          default_rps: 6,
        },
      });
      return;
    }
    if (path === "/api/v1/admin/indexing-schedules" && request.method() === "POST") {
      await route.fulfill({
        json: {
          id: 6,
          nexus_instance_id: 7,
          nexus_name: "local",
          nexus_base_url: "http://nexus:8081",
          scan_type: "verification",
          name: "verification-daily",
          enabled: true,
          schedule_expr: "every 86400s",
          interval_seconds: 86400,
          timezone: "UTC",
          scope: {},
          lock_conflict_behavior: "skip_blocked",
          created_at: now,
          updated_at: now,
        },
      });
      return;
    }
    if (path === "/api/v1/admin/tag-patterns" && request.method() === "POST") {
      await route.fulfill({
        json: {
          id: 3,
          name: "custom_feature",
          display_name: "Custom feature",
          description: "",
          tag_regex: "^custom[-/_].+$",
          priority: 500,
          enabled: true,
          built_in: false,
          created_at: now,
          updated_at: now,
        },
      });
      return;
    }
    if (path === "/api/v1/admin/tag-patterns/reclassify" && request.method() === "POST") {
      await route.fulfill({
        status: 202,
        json: {
          id: 88,
          nexus_instance_id: 7,
          scan_type: "tag_reclassification",
          status: "running",
          completed_tasks: 0,
          failed_tasks: 0,
          started_at: now,
        },
      });
      return;
    }
    if (path === "/api/v1/cleanup/simulations" && request.method() === "POST") {
      const body = request.postDataJSON() as { policy_yaml?: string };
      if (!body.policy_yaml?.includes("candidate_rules")) {
        await route.fulfill({
          status: 400,
          json: {
            code: "cleanup_policy_invalid",
            message: "at least one candidate rule is required",
            request_id: "req-sim-1",
            retryable: false,
          },
        });
        return;
      }
      await route.fulfill({
        json: {
          policy_name: "ci-feature-cleanup",
          evaluated_tag_count: 2,
          candidate_tag_count: 1,
          protected_tag_count: 1,
          warning_count: 1,
          estimated_reclaimable_bytes: 1073741824,
          candidates: [
            {
              tag_id: 101,
              image_id: 11,
              repository_id: 1,
              nexus_instance_id: 7,
              repository_name: "docker-hosted-a",
              image_name: "demo/app",
              tag: "feature-123",
              pattern_name: "feature_branch",
              rule_name: "old feature branches",
              reason: "pattern, older than 30d, never downloaded",
              docker_logical_bytes: 2684354560,
              estimated_reclaimable_bytes: 1073741824,
              first_seen_at: "2026-05-01T00:00:00Z",
              last_seen_at: now,
            },
          ],
          protected: [
            {
              tag_id: 102,
              image_id: 11,
              repository_id: 1,
              nexus_instance_id: 7,
              repository_name: "docker-hosted-a",
              image_name: "demo/app",
              tag: "latest",
              pattern_name: "channel",
              rule_name: "protected",
              reason: "matched protected rule",
              docker_logical_bytes: 2684354560,
              estimated_reclaimable_bytes: 0,
              first_seen_at: "2026-05-01T00:00:00Z",
              last_seen_at: now,
            },
          ],
          warnings: [
            {
              code: "missing_manifest",
              tag: {
                tag_id: 103,
                image_id: 11,
                repository_id: 1,
                nexus_instance_id: 7,
                repository_name: "docker-hosted-a",
                image_name: "demo/app",
                tag: "broken",
                pattern_name: "unclassified",
                docker_logical_bytes: 0,
                estimated_reclaimable_bytes: 0,
                first_seen_at: "2026-05-01T00:00:00Z",
                last_seen_at: now,
              },
            },
          ],
        },
      });
      return;
    }
    if (path === "/api/v1/cleanup/executions" && request.method() === "POST") {
      const body = request.postDataJSON() as { approval_text?: string };
      if (body.approval_text !== "ci-feature-cleanup") {
        await route.fulfill({
          status: 400,
          json: {
            code: "cleanup_execution_invalid",
            message: "approval_text must match policy name ci-feature-cleanup",
            request_id: "test",
            retryable: false,
          },
        });
        return;
      }
      await route.fulfill({
        status: 202,
        json: {
          id: 9,
          nexus_instance_id: 7,
          policy_name: "ci-feature-cleanup",
          status: "blocked_read_only",
          requested_by: "admin",
          approved_by: "admin",
          destructive_enabled: false,
          simulation_summary: { candidate_tag_count: 1 },
          audit_log: [{ event: "approval_recorded" }],
          created_at: now,
          approved_at: now,
          finished_at: now,
        },
      });
      return;
    }
    if (path === "/api/v1/cleanup/executions" && request.method() === "GET") {
      await route.fulfill({
        json: {
          executions: [
            {
              id: 9,
              nexus_instance_id: 7,
              policy_name: "ci-feature-cleanup",
              status: "blocked_read_only",
              requested_by: "admin",
              approved_by: "admin",
              destructive_enabled: false,
              simulation_summary: { candidate_tag_count: 1 },
              audit_log: [{ event: "approval_recorded" }],
              created_at: now,
              approved_at: now,
              finished_at: now,
            },
          ],
        },
      });
      return;
    }
    if (path === "/api/v1/cleanup/executions/9/events" && request.method() === "GET") {
      await route.fulfill({
        json: {
          events: [
            {
              id: 91,
              cleanup_execution_id: 9,
              event_type: "blocked_read_only",
              status: "blocked",
              actor: "admin",
              message: "destructive cleanup execution blocked because read-only mode is enabled",
              created_at: now,
            },
          ],
        },
      });
      return;
    }

    await route.fulfill({ status: 404, json: { code: "not_found" } });
  });
});

test("shows run status, queue, findings, and admin settings", async ({ page }) => {
  const schedulesRequest = page.waitForRequest((request) => {
    const url = new URL(request.url());
    return (
      url.pathname === "/api/v1/admin/indexing-schedules" &&
      request.method() === "GET" &&
      url.searchParams.get("nexus_instance_id") === "7"
    );
  });
  await page.goto("/");
  await schedulesRequest;

  await expect(page.getByText("API: ok", { exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Artifact overview" })).toBeVisible();
  await expect(page.locator(".metric").filter({ hasText: "Nexus asset bytes" })).toContainText("3.0 GiB");
  await expect(page.getByText("docker-hosted-a").first()).toBeVisible();
  await expect(page.getByText("demo/app:feature-123")).toBeVisible();
  await expect(page.getByRole("cell", { name: "never" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Package formats" })).toBeVisible();
  await expect(page.getByRole("cell", { name: "pypi-hosted" })).toBeVisible();
  await expect(page.getByRole("cell", { name: "asset_indexed" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Trends" })).toBeVisible();
  await expect(page.getByText("2026-06-27")).toBeVisible();
  await expect(page.getByText("missing", { exact: true })).toBeVisible();
  await expect(page.getByText("+2.0 GiB")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Tag patterns" })).toBeVisible();
  await expect(page.getByRole("cell", { name: "semver" }).first()).toBeVisible();
  await expect(page.getByRole("cell", { name: "demo", exact: true })).toBeVisible();
  await expect(page.getByText("demo/app:v1.2.3")).toBeVisible();
  await expect(page.getByRole("cell", { name: "Feature branch" })).toBeVisible();
  await expect(page.getByText("team-123-build")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Cleanup simulation" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Recent runs" })).toBeVisible();
  await expect(page.getByRole("button", { name: "42" })).toBeVisible();
  await expect(page.getByText("nexus_repository_list_network_failed")).toBeVisible();
  await expect(page.getByText("docker_tag_digest_missing")).toBeVisible();
  await expect(page.getByText("recent-sync")).toBeVisible();
  await expect(page.getByText("Admin API: ok")).toBeVisible();
});

test("toggles and persists light and dark mode", async ({ page }) => {
  await page.addInitScript(() => {
    if (!window.localStorage.getItem("nexus-lens-theme")) {
      window.localStorage.setItem("nexus-lens-theme", "light");
    }
  });
  await page.goto("/");

  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.getByRole("button", { name: "Dark mode" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(page.getByRole("button", { name: "Light mode" })).toBeVisible();

  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
});

test("requires typed confirmation for full reconcile", async ({ page }) => {
  await page.goto("/");
  await page.getByLabel("Run type").selectOption("full_reconcile");
  await page.getByRole("button", { name: "Start" }).click();

  const dialog = page.getByRole("dialog", { name: "Confirm index operation" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Start" })).toBeDisabled();

  await page.getByLabel("Type local to confirm").fill("local");
  await dialog.getByRole("button", { name: "Start" }).click();

  await expect(page.getByText("started full_reconcile run 77")).toBeVisible();
});

test("applies artifact explorer filters", async ({ page }) => {
  await page.goto("/");
  const explorer = page.locator("section").filter({
    has: page.getByRole("heading", { name: "Artifact explorer" }),
  });
  await explorer.getByLabel("Repository", { exact: true }).fill("docker-hosted-a");
  await explorer.getByLabel("Search image or tag").fill("feature");
  await explorer.getByLabel("Older than days").fill("14");
  await explorer.getByLabel("Query limit").fill("20");
  await explorer.getByRole("checkbox", { name: "Never downloaded" }).check();

  const tagRequest = page.waitForRequest((request) => {
    const url = new URL(request.url());
    return (
      url.pathname === "/api/v1/analytics/tags" &&
      url.searchParams.get("repository_name") === "docker-hosted-a" &&
      url.searchParams.get("search") === "feature" &&
      url.searchParams.get("older_than_days") === "14" &&
      url.searchParams.get("limit") === "20" &&
      url.searchParams.get("never_downloaded") === "true"
    );
  });
  await page.getByRole("button", { name: "Apply" }).click();
  await tagRequest;

  const growthRequest = page.waitForRequest((request) => {
    const url = new URL(request.url());
    return (
      url.pathname === "/api/v1/analytics/tag-patterns/growth" &&
      url.searchParams.get("repository_name") === "docker-hosted-a" &&
      url.searchParams.get("growth_window_days") === "30"
    );
  });
  const tagPatterns = page.locator("section").filter({
    has: page.getByRole("heading", { name: "Tag patterns" }),
  });
  await tagPatterns.getByRole("button", { name: "Refresh" }).click();
  await growthRequest;

  const trends = page.locator("section").filter({
    has: page.getByRole("heading", { name: "Trends" }),
  });
  await trends.getByLabel("Repository ID").fill("1");
  await trends.getByLabel("Image ID").fill("11");
  const trendRequest = page.waitForRequest((request) => {
    const url = new URL(request.url());
    return (
      url.pathname === "/api/v1/analytics/trends" &&
      url.searchParams.get("repository_id") === "1" &&
      url.searchParams.get("image_id") === "11" &&
      url.searchParams.get("metric_name") === "docker_logical_bytes"
    );
  });
  await trends.getByRole("button", { name: "Refresh" }).click();
  await trendRequest;
});

test("saves a tag pattern and starts reclassification", async ({ page }) => {
  await page.goto("/");
  await page.getByLabel("Pattern name").fill("custom_feature");
  await page.getByLabel("Display name").fill("Custom feature");
  await page.getByLabel("Regex").fill("^custom[-/_].+$");

  const saveRequest = page.waitForRequest((request) => {
    const url = new URL(request.url());
    return url.pathname === "/api/v1/admin/tag-patterns" && request.method() === "POST";
  });
  await page.getByRole("button", { name: "Save pattern" }).click();
  await saveRequest;
  await expect(page.getByText("saved tag pattern custom_feature")).toBeVisible();

  const reclassifyRequest = page.waitForRequest((request) => {
    const url = new URL(request.url());
    return url.pathname === "/api/v1/admin/tag-patterns/reclassify" && request.method() === "POST";
  });
  await page.getByRole("button", { name: "Reclassify" }).click();
  await reclassifyRequest;
  await expect(page.getByText("started tag_reclassification run 88")).toBeVisible();
});

test("saves retention settings as an admin", async ({ page }) => {
  await page.goto("/");
  const settings = page.locator("section").filter({
    has: page.getByRole("heading", { name: "Settings" }),
  });
  await expect(settings.getByText("Database override by admin")).toBeVisible();

  await settings.getByRole("spinbutton", { name: "Retention interval seconds", exact: true }).fill("7200");
  await settings.getByLabel("Scan run days").fill("45");
  await settings.getByLabel("Cleanup execution days").fill("400");
  await settings.getByLabel("Closed finding days").fill("120");
  await settings.getByLabel("Trend snapshot days").fill("730");

  const saveRequest = page.waitForRequest((request) => {
    const url = new URL(request.url());
    return url.pathname === "/api/v1/admin/retention-settings" && request.method() === "PUT";
  });
  await settings.getByRole("button", { name: "Save retention" }).click();
  const request = await saveRequest;
  const body = request.postDataJSON() as {
    interval_seconds: number;
    scan_run_days: number;
    cleanup_execution_days: number;
    verification_finding_days: number;
    trend_snapshot_days: number;
  };
  expect(body).toMatchObject({
    interval_seconds: 7200,
    scan_run_days: 45,
    cleanup_execution_days: 400,
    verification_finding_days: 120,
    trend_snapshot_days: 730,
  });

  await expect(page.getByText("saved retention settings")).toBeVisible();
  await expect(settings.getByText("Database override by admin")).toBeVisible();
});

test("operates indexing controls, queue retry, findings dismissal, schedules, and rate limits", async ({ page }) => {
  await page.goto("/");

  const pauseRequest = page.waitForRequest((request) => {
    const url = new URL(request.url());
    return url.pathname === "/api/v1/index/nexus/7/pause" && request.method() === "POST";
  });
  await page.getByRole("button", { name: "Pause" }).click();
  await pauseRequest;
  await expect(page.getByText("paused Nexus 7")).toBeVisible();

  const resumeRequest = page.waitForRequest((request) => {
    const url = new URL(request.url());
    return url.pathname === "/api/v1/index/nexus/7/resume" && request.method() === "POST";
  });
  await page.getByRole("button", { name: "Resume" }).click();
  await resumeRequest;
  await expect(page.getByText("resumed Nexus 7")).toBeVisible();

  const retryRequest = page.waitForRequest((request) => {
    const url = new URL(request.url());
    return url.pathname === "/api/v1/index/tasks/99/retry" && request.method() === "POST";
  });
  await page.getByRole("button", { name: "Retry" }).click();
  await retryRequest;
  await expect(page.getByText("queued retry for task 99")).toBeVisible();

  const dismissRequest = page.waitForRequest((request) => {
    const url = new URL(request.url());
    return url.pathname === "/api/v1/index/verification/findings/17/dismiss" && request.method() === "POST";
  });
  await page.getByRole("button", { name: "Dismiss" }).click();
  await dismissRequest;
  await expect(page.getByText("dismissed finding 17")).toBeVisible();

  const settings = page.locator("section").filter({
    has: page.getByRole("heading", { name: "Settings" }),
  });
  await settings.getByLabel("Schedule name").fill("bounded-weekly");
  await settings.getByLabel("Schedule type").selectOption("bounded_reindex");
  await settings.getByRole("spinbutton", { name: "Interval seconds", exact: true }).fill("604800");
  await settings.getByLabel("Run when due").check();
  const scheduleRequest = page.waitForRequest((request) => {
    const url = new URL(request.url());
    if (url.pathname !== "/api/v1/admin/indexing-schedules" || request.method() !== "POST") {
      return false;
    }
    const body = request.postDataJSON() as { name: string; scan_type: string; interval_seconds: number; due_now: boolean };
    return body.name === "bounded-weekly" && body.scan_type === "bounded_reindex" && body.interval_seconds === 604800 && body.due_now;
  });
  await settings.getByRole("button", { name: "Save schedule" }).click();
  await scheduleRequest;
  await expect(page.getByText("saved schedule bounded-weekly")).toBeVisible();

  await settings.getByLabel("Asset list RPS").fill("0");
  await settings.getByLabel("Manifest RPS").fill("2");
  await settings.getByLabel("Default RPS").fill("8");
  const rateLimitRequest = page.waitForRequest((request) => {
    const url = new URL(request.url());
    if (url.pathname !== "/api/v1/admin/nexus/7/rate-limits" || request.method() !== "PUT") {
      return false;
    }
    const body = request.postDataJSON() as {
      asset_list_rps?: number;
      registry_manifest_rps?: number;
      default_rps?: number;
    };
    return body.asset_list_rps === 0 && body.registry_manifest_rps === 2 && body.default_rps === 8;
  });
  await settings.getByRole("button", { name: "Save rate limits" }).click();
  await rateLimitRequest;
  await expect(page.getByText("saved rate limits for Nexus 7")).toBeVisible();
});

test("disables permission-sensitive controls for read-only users", async ({ page }) => {
  await page.route("http://127.0.0.1:5601/api/v1/me", async (route) => {
    await route.fulfill({
      json: {
        username: "reader",
        role: "read_only",
        capabilities: ["read"],
      },
    });
  });

  await page.goto("/");
  await page.getByLabel("Run type").selectOption("full_reconcile");
  await expect(page.getByRole("button", { name: "Start" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Save pattern" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Reclassify" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Save retention" })).toBeDisabled();

  const cleanup = page.locator("section").filter({
    has: page.getByRole("heading", { name: "Cleanup simulation" }),
  });
  await cleanup.getByRole("button", { name: "Simulate" }).click();
  await expect(cleanup.getByRole("button", { name: "Request execution" })).toBeDisabled();
});

test("validates and runs cleanup simulation", async ({ page }) => {
  await page.goto("/");
  const section = page.locator("section").filter({
    has: page.getByRole("heading", { name: "Cleanup simulation" }),
  });
  await section.getByLabel("Policy YAML").fill("name: invalid");
  await section.getByRole("button", { name: "Simulate" }).click();
  await expect(section.getByText("at least one candidate rule is required")).toBeVisible();
  await expect(section.getByText("code cleanup_policy_invalid")).toBeVisible();
  await expect(section.getByText("request req-sim-1")).toBeVisible();
  await expect(section.getByText("retryable false")).toBeVisible();

  await section.getByRole("button", { name: "Reset policy" }).click();
  const simulationRequest = page.waitForRequest((request) => {
    const url = new URL(request.url());
    return url.pathname === "/api/v1/cleanup/simulations" && request.method() === "POST";
  });
  await section.getByRole("button", { name: "Simulate" }).click();
  await simulationRequest;

  await expect(section.getByRole("cell", { name: "old feature branches" })).toBeVisible();
  await expect(section.getByText("demo/app:feature-123")).toBeVisible();
  await expect(section.getByText("demo/app:latest")).toBeVisible();
  await expect(section.getByText("missing_manifest")).toBeVisible();

  await section.getByLabel("Approval text").fill("ci-feature-cleanup");
  const executionRequest = page.waitForRequest((request) => {
    const url = new URL(request.url());
    return url.pathname === "/api/v1/cleanup/executions" && request.method() === "POST";
  });
  await section.getByRole("button", { name: "Request execution" }).click();
  await executionRequest;
  await expect(section.getByText("Execution 9: blocked_read_only")).toBeVisible();
});

test("cancels a running cleanup execution", async ({ page }) => {
  await page.route(/\/api\/v1\/cleanup\/executions(\?.*)?$/, async (route) => {
    await route.fulfill({
      json: {
        executions: [
          {
            id: 10,
            nexus_instance_id: 7,
            policy_name: "ci-feature-cleanup",
            status: "running",
            requested_by: "admin",
            approved_by: "admin",
            destructive_enabled: true,
            approved_candidate_count: 1,
            simulation_summary: { candidate_tag_count: 1 },
            audit_log: [{ event: "cleanup_started" }],
            created_at: now,
            approved_at: now,
            started_at: now,
            locked_by: "worker-1",
          },
        ],
      },
    });
  });
  await page.route(/\/api\/v1\/cleanup\/executions\/10\/events(\?.*)?$/, async (route) => {
    await route.fulfill({
      json: {
        events: [
          {
            id: 101,
            cleanup_execution_id: 10,
            event_type: "cleanup_started",
            status: "running",
            actor: "worker-1",
            message: "cleanup execution started",
            created_at: now,
          },
        ],
      },
    });
  });
  await page.route("**/api/v1/cleanup/executions/10/cancel", async (route) => {
    await route.fulfill({
      json: {
        id: 10,
        nexus_instance_id: 7,
        policy_name: "ci-feature-cleanup",
        status: "cancel_requested",
        requested_by: "admin",
        destructive_enabled: true,
        approved_candidate_count: 1,
        simulation_summary: { candidate_tag_count: 1 },
        audit_log: [{ event: "cancel_requested" }],
        created_at: now,
        cancel_requested_at: now,
      },
    });
  });

  await page.goto("/");
  const cleanup = page.locator("section").filter({
    has: page.getByRole("heading", { name: "Cleanup simulation" }),
  });
  await cleanup.getByRole("button", { name: "Refresh executions" }).click();
  await expect(cleanup.getByRole("row", { name: /10 running/ })).toBeVisible();

  const cancelRequest = page.waitForRequest((request) => {
    const url = new URL(request.url());
    return url.pathname === "/api/v1/cleanup/executions/10/cancel" && request.method() === "POST";
  });
  await cleanup.getByRole("row", { name: /10 running/ }).getByRole("button", { name: "Cancel" }).click();
  await cancelRequest;
  await expect(cleanup.getByText("Execution 10: cancel_requested")).toBeVisible();
});
