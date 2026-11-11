import { expect, owner, signUp, test, workspace } from "./fixtures";

test("sign up and create the first workspace", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("link", { name: "Create a workspace" }).click();
  await expect(page).toHaveURL(/\/signup/);

  await signUp(page, owner.name, owner.email, "short");
  await expect(page.getByText("Use at least 10 characters.")).toBeVisible();
  await signUp(page, owner.name, owner.email, owner.password);
  await expect(page).toHaveURL(/\/w$/);
  await expect(page.getByText("You’re not in a workspace yet")).toBeVisible();

  await page.getByLabel("Name").fill(workspace.name);
  await page.getByRole("button", { name: "Create workspace" }).click();
  await expect(page).toHaveURL(new RegExp(`/w/${workspace.slug}$`));
  await expect(page.getByRole("heading", { name: workspace.name })).toBeVisible();

  await page.context().storageState({ path: "e2e/.auth/owner.json" });
});
