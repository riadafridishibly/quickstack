import { createHmac } from "node:crypto";
import { test as base, expect, type Page } from "@playwright/test";

// Every test fails if the page logs a console error or throws, e.g. a CSP
// violation or an island that fails to load. HTTP error statuses are
// expected (422 for validation) and ignored.
export const test = base.extend<{ consoleErrors: string[] }>({
  consoleErrors: [
    async ({ page }, use) => {
      const errors: string[] = [];
      page.on("console", (msg) => {
        if (msg.type() === "error" && !msg.text().startsWith("Failed to load resource")) {
          errors.push(msg.text());
        }
      });
      page.on("pageerror", (err) => errors.push(err.message));
      await use(errors);
      expect(errors).toEqual([]);
    },
    { auto: true },
  ],
});

export { expect };

// The owner e2e/owner.setup.ts signs up, and the workspace they create.
export const owner = { name: "Olivia", email: "owner@example.com", password: "owner password 1" };
export const workspace = { name: "Acme Robotics", slug: "acme-robotics" };

// mailLink returns the link in the newest mail to `to` whose text contains
// `about`, from /dev/mail. A background job sends mail, so the page is
// reloaded until it shows up.
export async function mailLink(page: Page, to: string, about: string): Promise<string> {
  const mail = page
    .locator("li", { has: page.locator(`[data-to="${to}"]`), hasText: about })
    .first();
  await expect(async () => {
    await page.goto("/dev/mail");
    await expect(mail).toBeVisible({ timeout: 250 });
  }).toPass({ timeout: 10_000 });
  const link = ((await mail.textContent()) ?? "").match(/https?:\/\/\S+/)?.[0];
  if (!link) throw new Error(`no link in mail to ${to}`);
  return link;
}

// signUp creates an account through the form and leaves the page signed in.
export async function signUp(page: Page, name: string, email: string, password: string) {
  await page.getByLabel("Your name").fill(name);
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: "Create account" }).click();
}

// totp is the code an authenticator app shows at `at` for a key as the
// setup page prints it (RFC 6238: SHA-1, six digits, 30-second steps).
export function totp(key: string, at = Date.now()): string {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = "";
  for (const ch of key.replace(/\s/g, ""))
    bits += alphabet.indexOf(ch).toString(2).padStart(5, "0");
  const secret = Buffer.from(bits.match(/.{8}/g)?.map((b) => Number.parseInt(b, 2)) ?? []);
  const counter = Buffer.alloc(8);
  counter.writeBigInt64BE(BigInt(Math.floor(at / 30_000)));
  const mac = createHmac("sha1", secret).update(counter).digest();
  const off = (mac.at(-1) ?? 0) & 0x0f;
  return String((mac.readUInt32BE(off) & 0x7fffffff) % 1_000_000).padStart(6, "0");
}
