import { expect, mailLink, signUp, test, totp } from "./fixtures";

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

  await page.goto(await mailLink(page, "cy@example.com", "Reset your"));
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

test.describe(() => {
  // Sign-ups are limited per client, and the specs together pass the limit
  // from one address. The app trusts the proxy header in e2e, so these come
  // from another.
  test.use({ extraHTTPHeaders: { "X-Forwarded-For": "203.0.113.7" } });

  test("sign in with an authenticator app", async ({ page }) => {
    await page.goto("/signup");
    await signUp(page, "Tia", "tia@example.com", "tia password 1");
    await expect(page).toHaveURL(/\/w$/);

    await page.goto("/account/security");
    await page.getByRole("link", { name: "Set up" }).click();
    await expect(page.getByRole("img", { name: "QR code to scan" })).toBeVisible();
    const key = (await page.locator("#totp-key").textContent()) ?? "";
    await page.getByLabel("Code from your authenticator app").fill(totp(key));
    await page.getByRole("button", { name: "Turn on" }).click();
    await expect(page.getByRole("heading", { name: "Save your recovery codes" })).toBeVisible();
    await expect(page.locator("#recovery-codes li")).toHaveCount(10);
    await page.getByRole("link", { name: "I’ve saved them" }).click();
    await expect(page.getByText("10 of 10 codes left.")).toBeVisible();

    await page.getByRole("button", { name: "Sign out" }).click();
    await page.getByLabel("Email").fill("tia@example.com");
    await page.getByLabel("Password").fill("tia password 1");
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page.getByRole("heading", { name: "Verify it’s you" })).toBeVisible();
    // The code that turned the app on is spent, so use the next one.
    await page.getByLabel("Code from your authenticator app").fill(totp(key, Date.now() + 30_000));
    await page.getByRole("button", { name: "Verify" }).click();
    await expect(page).toHaveURL(/\/w$/);
  });
});
