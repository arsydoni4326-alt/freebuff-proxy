import { test, expect } from "@playwright/test";
import { loadFixtures, mockDashboard } from "./mocks.js";

const root = "http://127.0.0.1:4173/admin/";

// ARSYDONI UPDATE SOURCE: payload shape mirrors VersionResponse
// (backend/internal/dashboard/admin_wire.go).
const updatePayload = {
  current_version: "v1.12.2",
  has_update: true,
  latest_version: "v1.13.0",
  update_url: "https://github.com/arsydoni4326-alt/freebuff-proxy/releases",
  latest_commit: "0123456789abcdef0123456789abcdef01234567",
  changelog: "### Added\n- Update-available modal on every dashboard load.",
};

test.describe("arsydoni update modal", () => {
  test("opens on load while an update is available", async ({ page }) => {
    await mockDashboard(page, loadFixtures(), { version: updatePayload });
    await page.goto(root);

    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText("v1.13.0");
    await expect(dialog).toContainText("0123456");
    await expect(dialog).toContainText("Update-available modal");
    await expect(
      dialog.locator(
        'a[href="https://github.com/arsydoni4326-alt/freebuff-proxy/releases"]',
      ),
    ).toHaveCount(1);
  });

  test("dismiss closes it; the next page load reopens it while outdated", async ({
    page,
  }) => {
    await mockDashboard(page, loadFixtures(), { version: updatePayload });
    await page.goto(root);

    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    await dialog.getByRole("button", { name: "Later" }).click();
    await expect(dialog).toHaveCount(0);

    // A fresh load (refresh) reopens the modal: it is shown on every
    // refresh for as long as the gateway reports an update.
    await page.goto(root);
    await expect(page.getByRole("dialog")).toBeVisible();
  });

  test("stays closed when the gateway is current", async ({ page }) => {
    await mockDashboard(page, loadFixtures()); // fixture version: has_update false
    await page.goto(root);
    await expect(page.getByRole("dialog")).toHaveCount(0);
  });
});
