import { execFileSync } from "node:child_process";
import { expect, signUp, test } from "./fixtures";

test.use({ storageState: { cookies: [], origins: [] } });

// A 4×3 red PNG.
const png = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAQAAAADCAIAAAA7ljmRAAAAEElEQVR4nGP4z8AARww4OQD1MQv1NXv7ggAAAABJRU5ErkJggg==",
  "base64",
);

test("an editor uploads an image and sees it once processed", async ({ page }) => {
  await page.goto("/signup");
  await signUp(page, "Eve", "eve@example.com", "eve password 1");
  await expect(page).toHaveURL(/\/w$/);
  // `make e2e` exports the e2e database URLs, which the command uses too.
  execFileSync("./bin/quickstack", ["set-platform-role", "eve@example.com", "editor"]);

  await page.goto("/admin");
  await expect(page).toHaveURL(/\/admin\/media$/);

  const file = page.getByLabel("Upload an image");
  await file.setInputFiles({
    name: "notes.txt",
    mimeType: "text/plain",
    buffer: Buffer.from("hello"),
  });
  await page.getByRole("button", { name: "Upload", exact: true }).click();
  await expect(page.getByText("This file isn’t an image we can use.")).toBeVisible();
  await expect(file).toHaveAttribute("aria-invalid", "true");

  await file.setInputFiles({ name: "red.png", mimeType: "image/png", buffer: png });
  await page.getByRole("button", { name: "Upload", exact: true }).click();
  await expect(page).toHaveURL(/\/admin\/media\/[0-9a-z]{8}$/);

  // The page polls until the job has made the variants.
  const img = page.locator("#asset-preview img");
  await expect(img).toBeVisible({ timeout: 15_000 });
  await expect
    .poll(() => img.evaluate((el: HTMLImageElement) => el.complete && el.naturalWidth > 0))
    .toBe(true);

  await page.getByLabel("Alt text").fill("A red square");
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByText("Saved.")).toBeVisible();
  await expect(page.getByLabel("Markdown")).toHaveValue(
    /^!\[A red square\]\(\/media\/[0-9a-z]{8}\)$/,
  );
});
