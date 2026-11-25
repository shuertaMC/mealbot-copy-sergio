import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { expect, test } from "@playwright/test";

test.describe("Public static assets", () => {
  test("root lists the two public assets in origin order", async ({ page }) => {
    const response = await page.goto("/", { waitUntil: "networkidle" });

    expect(response?.status()).toBe(200);
    await expect(page.locator('a[href="privacy.html"]')).toHaveText("privacy.html");
    await expect(page.locator('a[href="sample.csv"]')).toHaveText("sample.csv");
    await expect(page.locator("body a")).toHaveCount(2);
    expect(await page.locator("body a").allTextContents()).toEqual([
      "privacy.html",
      "sample.csv",
    ]);
    expect((await page.locator("body").innerText()).trim()).toBe(
      "privacy.html\nsample.csv",
    );
    await expect(page.locator("h1, h2, h3, h4, h5, h6")).toHaveCount(0);
    await expect(page).toHaveTitle("");
    expect(
      await page.locator("body").evaluate((body) => getComputedStyle(body).fontFamily),
    ).toBe('"Times New Roman"');
  });

  test("privacy policy renders publicly with its external links and styles", async ({
    page,
  }) => {
    const response = await page.goto("/privacy.html", { waitUntil: "networkidle" });

    expect(response?.status()).toBe(200);
    await expect(page).toHaveTitle("Privacy Policy");
    await expect(page.getByRole("heading", { level: 2, name: "Privacy Policy" })).toBeVisible();

    const expectedLinks = [
      {
        href: "https://johnamadeo.github.io",
        name: "(johnamadeo.github.io)",
      },
      {
        href: "https://privacypolicytemplate.net",
        name: "privacypolicytemplate.net",
      },
      {
        href: "https://app-privacy-policy-generator.firebaseapp.com/",
        name: "App Privacy Policy Generator",
      },
    ];

    for (const expectedLink of expectedLinks) {
      const link = page.locator(`a[href="${expectedLink.href}"]`);
      await expect(link).toHaveAccessibleName(expectedLink.name);
      await expect(link).toHaveAttribute("target", "_blank");
    }
    await expect(page.locator("body a")).toHaveCount(3);

    const bodyStyles = await page.locator("body").evaluate((body) => {
      const styles = getComputedStyle(body);
      return {
        backgroundColor: styles.backgroundColor,
        fontFamily: styles.fontFamily,
        padding: styles.padding,
      };
    });
    expect(bodyStyles).toEqual({
      backgroundColor: "rgba(0, 0, 0, 0)",
      fontFamily: '"Helvetica Neue", Helvetica, Arial, sans-serif',
      padding: "16px",
    });
    expect(
      await page.locator("h2").evaluate((heading) => getComputedStyle(heading).fontSize),
    ).toBe("24px");
  });

  test("sample CSV downloads with the preserved filename and exact content", async ({
    page,
  }) => {
    await page.goto("/", { waitUntil: "networkidle" });

    const downloadPromise = page.waitForEvent("download", { timeout: 1_500 });
    const responsePromise = page.waitForResponse(
      (response) => new URL(response.url()).pathname === "/sample.csv",
      { timeout: 1_500 },
    );
    const [download, response] = await Promise.all([
      downloadPromise,
      responsePromise,
      page.getByRole("link", { name: "sample.csv" }).click(),
    ]);

    expect(response.status()).toBe(200);
    expect(response.headers()["content-type"]).toBe("text/csv; charset=utf-8");
    expect(response.headers()["content-disposition"]).toBeUndefined();
    expect(download.suggestedFilename()).toBe("sample.csv");

    const downloadPath = await download.path();
    expect(downloadPath).not.toBeNull();
    const content = await readFile(downloadPath!);
    expect(content.byteLength).toBe(155);
    expect(createHash("sha256").update(content).digest("hex")).toBe(
      "bc8652405a29f77ae729aa6da49f2603326b1d94d2bed60117c99cfcb32e9cd1",
    );
    expect(content.toString("utf8")).toBe(
      "Name,Email,College,Year\n" +
        "John Yale,johnamadeo.daniswara@yale.edu,SM,2019\n" +
        "John Gmail,johnamadeo.daniswara@gmail.com,SM,2019\n" +
        "Jadk Gmail,jjj@gmail.com,SM,2019\n",
    );
    await expect(page).toHaveURL(/\/$/);
    expect((await page.locator("body").innerText()).trim()).toBe(
      "privacy.html\nsample.csv",
    );
  });

  test("missing static asset returns the plain public 404", async ({ page }) => {
    const response = await page.goto("/not-found.txt", { waitUntil: "networkidle" });

    expect(response?.status()).toBe(404);
    expect((await page.locator("body").innerText()).trim()).toBe("404 page not found");
    await expect(
      page.locator('a, button, input, select, textarea, [role="button"]'),
    ).toHaveCount(0);
  });
});
