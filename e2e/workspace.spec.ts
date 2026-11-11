import { expect, mailLink, signUp, test, workspace } from "./fixtures";

const base = `/w/${workspace.slug}`;

test("invite someone, who signs up from the email and joins", async ({ page, browser }) => {
  await page.goto(`${base}/members`);
  await page.getByLabel("Email").fill("bo@example.com");
  await page.getByLabel("Role", { exact: true }).selectOption("member");
  await page.getByRole("button", { name: "Send invitation" }).click();
  await expect(page.getByText("Invitation sent to bo@example.com.")).toBeVisible();
  await expect(page.getByText("Waiting to accept")).toBeVisible();

  const link = await mailLink(page, "bo@example.com");
  const bo = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  const p = await bo.newPage();
  await p.goto(link);
  await expect(p.getByRole("heading", { name: `Join ${workspace.name}` })).toBeVisible();
  await p.getByRole("link", { name: "Create an account" }).click();
  await expect(p.getByLabel("Email")).toHaveValue("bo@example.com");
  await signUp(p, "Bo", "bo@example.com", "bo password 12");
  await p.getByRole("button", { name: "Join workspace" }).click();
  await expect(p).toHaveURL(new RegExp(`${base}$`));
  // A member doesn't see Settings.
  await expect(p.getByRole("link", { name: "Settings" })).toHaveCount(0);
  await bo.close();

  await page.goto(`${base}/members`);
  const row = page.locator('tr[data-member="bo@example.com"]');
  await expect(row).toBeVisible();
  await row.getByLabel("Role of Bo").selectOption("viewer");
  await expect(row.getByLabel("Role of Bo")).toHaveValue("viewer");
  await page.reload();
  await expect(
    page.locator('tr[data-member="bo@example.com"]').getByLabel("Role of Bo"),
  ).toHaveValue("viewer");
});

test("switch the overview chart's range", async ({ page }) => {
  await page.goto(base);
  await page.getByRole("link", { name: "7 days" }).click();
  await expect(page).toHaveURL(/days=7/);
  await expect(page.getByRole("link", { name: "7 days" })).toHaveAttribute("aria-current", "page");
});

test("publish the roadmap", async ({ page, browser }) => {
  await page.goto(`${base}/projects/new`);
  await page.getByLabel("Name").fill("Public launch");
  await page.getByLabel("Show on the public roadmap").check();
  await page.getByRole("button", { name: "Create project" }).click();
  await page.getByLabel("New task").fill("Announce it");
  await page.getByRole("button", { name: "Add task" }).click();
  await expect(page.locator("task-board").getByText("Announce it")).toBeVisible();

  await page.goto(`${base}/settings`);
  await page.getByLabel("Public roadmap").check();
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByText("Settings saved.")).toBeVisible();

  const visitor = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  const p = await visitor.newPage();
  await p.goto(`/p/${workspace.slug}`);
  await expect(p.getByRole("heading", { name: "Public launch" })).toBeVisible();
  await expect(p.getByText("Announce it")).toBeVisible();
  await expect(p.getByText("Website relaunch")).toHaveCount(0);
  await visitor.close();
});
