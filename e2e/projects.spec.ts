import { expect, test, workspace } from "./fixtures";

const base = `/w/${workspace.slug}`;

test("create a project, add tasks with htmx, and drag one across the board", async ({ page }) => {
  await page.goto(`${base}/projects`);
  await page.getByRole("link", { name: "New project" }).click();
  await page.getByRole("button", { name: "Create project" }).click();
  await expect(page.getByText("Enter a name.")).toBeVisible();
  await page.getByLabel("Name").fill("Website relaunch");
  await page.getByRole("button", { name: "Create project" }).click();
  await expect(page.getByRole("heading", { name: "Website relaunch" })).toBeVisible();
  await expect(page.locator("task-board[data-mounted]")).toBeVisible();

  for (const title of ["Write copy", "Pick photos"]) {
    await page.getByLabel("New task").fill(title);
    await page.getByRole("button", { name: "Add task" }).click();
    await expect(page.locator("task-board[data-mounted]").getByText(title)).toBeVisible();
    await expect(page.getByLabel("New task")).toHaveValue("");
  }

  const doing = page.locator('task-board [data-column="doing"]');
  const done = page.locator('task-board [data-column="done"]');
  await page.locator('task-board [data-task="Write copy"]').dragTo(doing);
  await expect(doing.locator('[data-task="Write copy"]')).toBeVisible();
  await page.getByRole("button", { name: "Move “Pick photos” to In progress" }).click();
  await page.getByRole("button", { name: "Move “Pick photos” to Done" }).click();
  await expect(done.locator('[data-task="Pick photos"]')).toBeVisible();

  // The moves were saved, not just shown.
  await page.reload();
  await expect(
    page.locator('task-board [data-column="doing"] [data-task="Write copy"]'),
  ).toBeVisible();
  await expect(
    page.locator('task-board [data-column="done"] [data-task="Pick photos"]'),
  ).toBeVisible();
});

test("search projects as you type", async ({ page }) => {
  await page.goto(`${base}/projects`);
  for (const name of ["Mobile app", "Warehouse robots"]) {
    await page.goto(`${base}/projects/new`);
    await page.getByLabel("Name").fill(name);
    await page.getByRole("button", { name: "Create project" }).click();
    await expect(page.getByRole("heading", { name })).toBeVisible();
  }
  await page.goto(`${base}/projects`);
  await page.getByPlaceholder("Search projects").fill("robots");
  await expect(page).toHaveURL(/q=robots/);
  await expect(page.getByRole("link", { name: "Warehouse robots" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Mobile app" })).toHaveCount(0);
});

test.describe("without JavaScript", () => {
  test.use({ javaScriptEnabled: false });

  test("forms and the board still work", async ({ page }) => {
    await page.goto(`${base}/projects/new`);
    await page.getByLabel("Name").fill("No-JS project");
    await page.getByRole("button", { name: "Create project" }).click();
    await expect(page.getByRole("heading", { name: "No-JS project" })).toBeVisible();

    await page.getByLabel("New task").fill("Plain task");
    await page.getByRole("button", { name: "Add task" }).click();
    const card = page.locator('[data-task="Plain task"]');
    await expect(card).toBeVisible();
    await card.getByLabel("Move Plain task").selectOption("done");
    await card.getByRole("button", { name: "Move" }).click();
    await expect(page.locator('[data-column="done"] [data-task="Plain task"]')).toBeVisible();
  });
});
