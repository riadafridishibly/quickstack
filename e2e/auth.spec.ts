import { expect, mailLink, signUp, test } from "./fixtures";

test.use({ storageState: { cookies: [], origins: [] } });

test("signed-out visitors are sent to sign in, and back", async ({ page }) => {
  await page.goto("/account");
  await expect(page).toHaveURL(/\/login\?next=%2Faccount$/);
});

test("reset a forgotten password from the email", async ({ page }) => {
  await page.goto("/signup");
  await signUp(page, "Cy", "cy@example.com", "first password");
  await expect(page).toHaveURL(/\/w$/);
  await page.getByRole("button", { name: "Sign out" }).click();

  await page.getByRole("link", { name: "Forgot your password?" }).click();
  await page.getByLabel("Email").fill("cy@example.com");
  await page.getByRole("button", { name: "Send reset link" }).click();
  await expect(page.getByText("Check your email")).toBeVisible();

  await page.goto(await mailLink(page, "cy@example.com"));
  await page.getByLabel("New password", { exact: true }).fill("second password");
  await page.getByLabel("Confirm new password").fill("second password");
  await page.getByRole("button", { name: "Set password and sign in" }).click();
  await expect(page).toHaveURL(/\/w$/);

  await page.getByRole("button", { name: "Sign out" }).click();
  await page.getByLabel("Email").fill("cy@example.com");
  await page.getByLabel("Password").fill("second password");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page).toHaveURL(/\/w$/);
});

test("the platform admin is for staff only", async ({ page }) => {
  await page.goto("/signup");
  await signUp(page, "Dee", "dee@example.com", "dee password 1");
  await page.goto("/admin");
  await expect(
    page.getByRole("heading", { name: "You don’t have access to this page" }),
  ).toBeVisible();
});
