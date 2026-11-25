import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { expect, test } from "@playwright/test";

const SECTION_LABELS = [
  "Information Collection and Use",
  "Log Data",
  "Cookies",
  "Service Providers",
  "Security",
  "Links to Other Sites",
  "Children’s Privacy",
  "Changes to This Privacy Policy",
  "Contact Us",
];

const SAMPLE_CSV = [
  "Name,Email,College,Year",
  "John Yale,johnamadeo.daniswara@yale.edu,SM,2019",
  "John Gmail,johnamadeo.daniswara@gmail.com,SM,2019",
  "Jadk Gmail,jjj@gmail.com,SM,2019",
  "",
].join("\n");

test("public root exposes only the privacy and sample links and navigates to privacy", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const response = await page.goto("/");
  await page.waitForLoadState("networkidle");

  expect(response?.status()).toBe(200);
  await expect(page).toHaveTitle("");
  await expect(page.locator("body")).toHaveText("privacy.html\nsample.csv");
  await expect(page.locator('a[href="privacy.html"]')).toHaveText("privacy.html");
  await expect(page.locator('a[href="sample.csv"]')).toHaveText("sample.csv");
  await expect(page.locator("h1, h2, h3, h4, h5, h6")).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(1440);

  await page.locator('a[href="privacy.html"]').click();
  await page.waitForLoadState("networkidle");
  await expect(page).toHaveURL(/\/privacy\.html$/);
  await expect(page.getByRole("heading", { level: 2, name: "Privacy Policy", exact: true })).toBeVisible();
});

test("privacy policy is complete and correctly presented on desktop", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const response = await page.goto("/privacy.html");
  await page.waitForLoadState("networkidle");

  expect(response?.status()).toBe(200);
  await expect(page).toHaveTitle("Privacy Policy");
  await expect(page.getByRole("heading", { level: 2, name: "Privacy Policy", exact: true })).toBeVisible();
  await expect(page.locator("p")).toHaveCount(27);
  await expect(page.locator("strong")).toHaveText(SECTION_LABELS);
  await expect(page.locator("body")).toContainText("John Amadeo Daniswara");
  await expect(page.locator("body")).toContainText("do not hesitate to contact me");
  await expect(page.locator("body")).toContainText("This privacy policy page was created at");

  const externalLinks = [
    ["https://johnamadeo.github.io", "(johnamadeo.github.io)"],
    ["https://privacypolicytemplate.net", "privacypolicytemplate.net"],
    ["https://app-privacy-policy-generator.firebaseapp.com/", "App Privacy Policy Generator"],
  ];
  for (const [href, text] of externalLinks) {
    const link = page.locator(`a[href="${href}"]`);
    await expect(link).toHaveText(text);
    await expect(link).toHaveAttribute("target", "_blank");
  }

  const presentation = await page.evaluate(() => {
    const style = getComputedStyle(document.body);
    return {
      fontFamily: style.fontFamily,
      padding: style.padding,
      scrollWidth: document.documentElement.scrollWidth,
      clientWidth: document.documentElement.clientWidth,
    };
  });
  expect(presentation).toEqual({
    fontFamily: '"Helvetica Neue", Helvetica, Arial, sans-serif',
    padding: "16px",
    scrollWidth: 1440,
    clientWidth: 1440,
  });
});

test("privacy policy remains readable without horizontal overflow on mobile", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/privacy.html");
  await page.waitForLoadState("networkidle");

  await expect(page.locator('meta[name="viewport"]')).toHaveAttribute("content", "width=device-width");
  await expect(page.getByRole("heading", { level: 2, name: "Privacy Policy", exact: true })).toBeVisible();
  await expect(page.locator("p")).toHaveCount(27);
  await expect(page.locator("strong")).toHaveText(SECTION_LABELS);

  const layout = await page.evaluate(() => {
    const body = document.body;
    const bodyRect = body.getBoundingClientRect();
    const style = getComputedStyle(body);
    const linksFit = Array.from(document.querySelectorAll("a")).every(
      (link) => link.getBoundingClientRect().right <= document.documentElement.clientWidth,
    );
    return {
      viewportWidth: document.documentElement.clientWidth,
      scrollWidth: document.documentElement.scrollWidth,
      bodyWidth: bodyRect.width,
      bodyLeft: bodyRect.left,
      bodyRightGap: document.documentElement.clientWidth - bodyRect.right,
      fontSize: style.fontSize,
      linksFit,
    };
  });
  expect(layout).toEqual({
    viewportWidth: 390,
    scrollWidth: 390,
    bodyWidth: 374,
    bodyLeft: 8,
    bodyRightGap: 8,
    fontSize: "16px",
    linksFit: true,
  });
});

test("sample CSV downloads without authentication and preserves its exact bytes", async ({ page, request }) => {
  const response = await request.get("/sample.csv");
  expect(response.status()).toBe(200);
  expect(response.headers()["content-type"]).toBe("text/csv; charset=utf-8");
  expect(response.headers()["content-length"]).toBe("155");
  expect((await response.body()).equals(Buffer.from(SAMPLE_CSV))).toBe(true);

  await page.goto("/");
  await page.waitForLoadState("networkidle");
  const downloadPromise = page.waitForEvent("download");
  await page.locator('a[href="sample.csv"]').click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toBe("sample.csv");
  const path = await download.path();
  expect(path).not.toBeNull();
  const bytes = await readFile(path!);
  expect(bytes.toString("utf8")).toBe(SAMPLE_CSV);
  expect(createHash("sha256").update(bytes).digest("hex")).toBe(
    "bc8652405a29f77ae729aa6da49f2603326b1d94d2bed60117c99cfcb32e9cd1",
  );
  await expect(page).toHaveURL(/\/$/);
});
