import { execFileSync } from "node:child_process";
import { crc32, deflateSync } from "node:zlib";
import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";

test.use({ storageState: { cookies: [], origins: [] } });

// solidPNG is a w×h PNG of one opaque colour.
function solidPNG(w: number, h: number, [r, g, b]: [number, number, number]): Buffer {
  const chunk = (type: string, data: Buffer) => {
    const out = Buffer.alloc(12 + data.length);
    out.writeUInt32BE(data.length, 0);
    out.write(type, 4, "latin1");
    data.copy(out, 8);
    out.writeUInt32BE(crc32(out.subarray(4, 8 + data.length)), 8 + data.length);
    return out;
  };
  const header = Buffer.alloc(13);
  header.writeUInt32BE(w, 0);
  header.writeUInt32BE(h, 4);
  header[8] = 8; // bit depth
  header[9] = 2; // RGB
  const row = Buffer.alloc(1 + w * 3);
  for (let x = 0; x < w; x++) row.set([r, g, b], 1 + x * 3);
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", header),
    chunk("IDAT", deflateSync(Buffer.concat(Array(h).fill(row)))),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

// upload uploads a PNG in Media and returns its address.
async function upload(page: Page, name: string, data: Buffer): Promise<string> {
  await page.goto("/admin/media");
  await page
    .getByLabel("Upload an image")
    .setInputFiles({ name, mimeType: "image/png", buffer: data });
  await page.getByRole("button", { name: "Upload", exact: true }).click();
  await expect(page).toHaveURL(/\/admin\/media\/[0-9a-z]{8}$/);
  await expect(page.locator("#asset-preview img")).toBeVisible({ timeout: 15_000 });
  return new URL(page.url()).pathname.replace("/admin", "");
}

test("a superadmin sets the site icon", async ({ page }) => {
  // The account comes from the command line, since the specs' sign-ups
  // together would reach the limit on sign-ups from one address.
  // `make e2e` exports the e2e database URLs, which the commands use too.
  const created = execFileSync("./bin/quickstack", [
    "create-user",
    "ada@example.com",
    "Ada",
  ]).toString();
  const password = created.match(/password: (\S+)/)?.[1] ?? "";
  execFileSync("./bin/quickstack", ["set-platform-role", "ada@example.com", "superadmin"]);
  await page.goto("/login");
  await page.getByLabel("Email").fill("ada@example.com");
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page).toHaveURL(/\/w$/);

  const small = await upload(page, "small.png", solidPNG(40, 40, [0, 0, 255]));
  const icon = await upload(page, "icon.png", solidPNG(600, 600, [204, 0, 0]));

  await page.goto("/admin");
  await page.getByRole("link", { name: "Site", exact: true }).click();
  await expect(page).toHaveURL(/\/admin\/site$/);
  await expect(page.getByText("No icon is set")).toBeVisible();

  const image = page.getByLabel("Image");
  await image.fill(small);
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByText("This image is 40×40 pixels.")).toBeVisible();
  await expect(image).toHaveAttribute("aria-invalid", "true");
  await expect(page).toHaveURL(/\/admin\/site$/);

  await image.fill(icon);
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page).toHaveURL(/\/admin\/site\?done=saved$/);
  await expect(page.getByText("Icon saved.")).toBeVisible();
  // The status polls until the job has drawn the files.
  await expect(page.getByText("Pages show this icon.")).toBeVisible({ timeout: 15_000 });

  await page.goto("/blog");
  const apple = page.locator('link[rel="apple-touch-icon"]');
  await expect(apple).toHaveAttribute("href", /^\/site\/icon\/\d+\/apple-touch-icon\.png$/);
  const favicon = await page.request.get("/favicon.ico");
  expect(favicon.status()).toBe(200);
  expect(favicon.headers()["content-type"]).toBe("image/x-icon");
  const manifest = await (await page.request.get("/manifest.webmanifest")).json();
  expect(manifest.icons.map((i: { sizes: string }) => i.sizes)).toEqual(["192x192", "512x512"]);
});
