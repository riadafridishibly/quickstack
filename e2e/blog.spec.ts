import { execFileSync } from "node:child_process";
import type { Page } from "@playwright/test";
import { expect, signUp, test } from "./fixtures";

test.use({ storageState: { cookies: [], origins: [] } });

// A 4×3 red PNG.
const png = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAQAAAADCAIAAAA7ljmRAAAAEElEQVR4nGP4z8AARww4OQD1MQv1NXv7ggAAAABJRU5ErkJggg==",
  "base64",
);

async function signUpEditor(page: Page, name: string, email: string) {
  await page.goto("/signup");
  await signUp(page, name, email, `${name} password 1`);
  await expect(page).toHaveURL(/\/w$/);
  // `make e2e` exports the e2e database URLs, which the command uses too.
  execFileSync("./bin/quickstack", ["set-platform-role", email, "editor"]);
}

// paste pastes a file into the editor, as the clipboard would.
async function paste(page: Page, file: { name: string; type: string; data: Buffer }) {
  await page.getByRole("textbox", { name: "Body" }).evaluate(
    (el, f) => {
      const dt = new DataTransfer();
      dt.items.add(new File([new Uint8Array(f.data)], f.name, { type: f.type }));
      el.dispatchEvent(
        new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true }),
      );
    },
    { ...file, data: [...file.data] },
  );
}

test("the editor previews as you type and uploads pasted images", async ({ page }) => {
  await signUpEditor(page, "Wren", "wren@example.com");
  await page.goto("/admin/blog/new");
  await expect(page.locator("markdown-editor[data-mounted]")).toBeAttached();

  const body = page.getByRole("textbox", { name: "Body" });
  const preview = page.locator("#post-preview");
  await body.click();
  await page.keyboard.type("Hello ");
  await page.keyboard.press("ControlOrMeta+b");
  await page.keyboard.type("world");
  await expect(preview.locator("strong")).toHaveText("world");

  await page.keyboard.press("End");
  await paste(page, { name: "notes.txt", type: "text/plain", data: Buffer.from("hello") });
  await expect(page.getByRole("alert")).toContainText(
    "notes.txt: This file isn’t an image we can use.",
  );
  await expect(body).not.toContainText("Uploading");

  await paste(page, { name: "red.png", type: "image/png", data: png });
  const source = page.locator("textarea[name=body]");
  await expect(source).toHaveValue(/!\[\]\(\/media\/[0-9a-z]{8}\)/);
  // The preview shows the image once the job has made its variants.
  const img = preview.locator("img");
  await expect(img).toBeVisible({ timeout: 15_000 });
  await expect(preview.locator("[data-processing]")).toHaveCount(0);
  await expect
    .poll(() => img.evaluate((el: HTMLImageElement) => el.complete && el.naturalWidth > 0))
    .toBe(true);

  await page.getByLabel("Title").fill("Pasted pictures");
  await body.click();
  await page.keyboard.press("ControlOrMeta+s");
  await expect(page).toHaveURL(/\/admin\/blog\/\d+\?done=saved$/);
  await expect(page.getByText("Saved.")).toBeVisible();
  await expect(page.locator("textarea[name=body]")).toHaveValue(
    /^Hello \*\*world\*\*\n\n!\[\]\(\/media\/[0-9a-z]{8}\)$/,
  );

  const insert = page.locator("summary", { hasText: "Insert" });
  await insert.click();
  await page.getByRole("button", { name: "Tip", exact: true }).click();
  await expect(page.getByRole("button", { name: "Tip", exact: true })).toBeHidden();
  await expect(preview.getByRole("note")).toContainText("Tip");
  await insert.click();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("button", { name: "Tip", exact: true })).toBeHidden();
});

test("the editor switches views and goes full screen", async ({ page }) => {
  await signUpEditor(page, "Ash", "ash@example.com");
  await page.goto("/admin/blog/new");
  await expect(page.locator("markdown-editor[data-mounted]")).toBeAttached();

  const body = page.getByRole("textbox", { name: "Body" });
  const preview = page.locator("#post-preview");
  await body.click();
  await page.keyboard.type("Some *words*");
  await expect(preview.locator("em")).toHaveText("words");

  await page.getByRole("button", { name: "Preview", exact: true }).click();
  await expect(body).toBeHidden();
  await expect(preview).toBeVisible();
  await page.getByRole("button", { name: "Write", exact: true }).click();
  await expect(body).toBeVisible();
  await expect(preview).toBeHidden();
  await page.getByRole("button", { name: "Split", exact: true }).click();
  await expect(body).toBeVisible();
  await expect(preview).toBeVisible();

  await page.getByRole("button", { name: "Full screen" }).click();
  const viewport = page.viewportSize();
  await expect
    .poll(() => page.locator("markdown-editor").boundingBox())
    .toEqual({ x: 0, y: 0, width: viewport?.width, height: viewport?.height });
  const editorBox = await page.locator(".cm-editor").boundingBox();
  expect(editorBox?.height).toBeGreaterThan((viewport?.height ?? 0) * 0.7);

  // A save that fails leaves full screen, to show what's wrong.
  await body.click();
  await page.keyboard.press("ControlOrMeta+s");
  await expect(page.locator("markdown-editor[data-fullscreen]")).toHaveCount(0);
  await expect(page.locator("#title-error")).toBeVisible();

  // One that works keeps it across the reload.
  await page.getByRole("button", { name: "Full screen" }).click();
  await page.getByLabel("Title").fill("Full screen");
  await body.click();
  await page.keyboard.press("ControlOrMeta+s");
  await expect(page).toHaveURL(/\/admin\/blog\/\d+\?done=saved$/);
  await expect(page.locator("markdown-editor[data-fullscreen][data-mounted]")).toBeAttached();
  const note = page.locator("markdown-editor").getByRole("status");
  await expect(note).toHaveText("Saved.");
  await body.click();
  await page.keyboard.type(" More.");
  await expect(note).toHaveCount(0);

  await page.keyboard.press("Escape");
  await expect(page.locator("markdown-editor[data-fullscreen]")).toHaveCount(0);
  await expect(page.getByText("Saved.")).toBeVisible();

  // On a narrow screen Split isn't offered and Write shows.
  await page.setViewportSize({ width: 600, height: 800 });
  await expect(page.getByRole("button", { name: "Split", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Write", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await expect(preview).toBeHidden();
});

// The players run in their iframes and log to the same console, which the
// fixture checks. With no network the frames stay empty and this passes.
test("a post with video embeds loads without console errors", async ({ page }) => {
  await signUpEditor(page, "Kit", "kit@example.com");
  await page.goto("/admin/blog/new");
  await page.getByLabel("Title").fill("Videos");
  await expect(page.locator("markdown-editor[data-mounted]")).toBeAttached();
  await page.getByRole("textbox", { name: "Body" }).click();
  await page.keyboard.insertText(
    '{{< youtube id="f6kdp27TYZs" title="A talk" >}}\n\n{{< vimeo id="76979871" title="A demo" >}}',
  );
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page).toHaveURL(/\/admin\/blog\/\d+\?done=published$/);

  await page.goto("/blog/videos");
  for (const title of ["A talk", "A demo"]) {
    await page.locator(`iframe[title="${title}"]`).scrollIntoViewIfNeeded();
  }
  await page.waitForLoadState("networkidle");
});

test.describe("without JavaScript", () => {
  test.use({ javaScriptEnabled: false });

  test("a post is written, previewed and published", async ({ page }) => {
    await signUpEditor(page, "Pip", "pip@example.com");
    await page.goto("/admin/blog/new");
    await page.getByLabel("Title").fill("Plain words");
    await page.getByLabel("Body").fill("Written *without* JavaScript.");

    const [tab] = await Promise.all([
      page.waitForEvent("popup"),
      page.getByRole("button", { name: "Preview" }).click(),
    ]);
    await expect(tab.locator("article em")).toHaveText("without");
    await tab.close();

    await page.getByRole("button", { name: "Save draft" }).click();
    await expect(page.getByText("Saved.")).toBeVisible();
    expect((await page.request.get("/blog/plain-words")).status()).toBe(404);

    await page.getByRole("button", { name: "Publish" }).click();
    await expect(page.getByText("Published.")).toBeVisible();
    await page.goto("/blog/plain-words");
    await expect(page.getByRole("heading", { name: "Plain words" })).toBeVisible();
    await expect(page.locator("article em")).toHaveText("without");
  });
});
