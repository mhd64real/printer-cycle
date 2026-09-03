/**
 * Screenshots the dashboard, signed in.
 *
 * Exists because markup does not imply appearance. A page can typecheck, build,
 * serve, and still be unreadable, and the only way to know is to look at it.
 *
 * Uses puppeteer-core against the Chrome already on the machine rather than
 * downloading one: this is a development aid, not a dependency of the product.
 *
 *   node scripts/shot.mjs <url> <out.png> [username] [password]
 */
import puppeteer from "puppeteer-core";

const [url, out, username, password] = process.argv.slice(2);
if (!url || !out) {
  console.error("usage: node scripts/shot.mjs <url> <out.png> [username] [password]");
  process.exit(2);
}

const CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";

const browser = await puppeteer.launch({
  executablePath: CHROME,
  headless: true,
  args: ["--no-sandbox", "--hide-scrollbars"],
});

try {
  const page = await browser.newPage();
  await page.setViewport({ width: 1000, height: 820, deviceScaleFactor: 2 });

  await page.goto(url, { waitUntil: "networkidle2" });

  if (username && password) {
    // Signing in the way a person would, so what is captured is what they see.
    await page.waitForSelector('input[autocomplete="username"]', { timeout: 10_000 });
    await page.type('input[autocomplete="username"]', username);
    await page.type('input[type="password"]', password);
    await Promise.all([
      page.click('button[type="submit"]'),
      page.waitForFunction(() => !document.querySelector('button[type="submit"]'), {
        timeout: 15_000,
      }),
    ]);
  }

  // Optionally press something first, so a screenshot can capture a state that
  // only exists after an interaction.
  if (process.env.SHOT_CLICK) {
    await clickByText(page, process.env.SHOT_CLICK);
  }

  await new Promise((r) => setTimeout(r, Number(process.env.SHOT_WAIT ?? 1000)));

  if (process.env.SHOT_THEN_CLICK) {
    await clickByText(page, process.env.SHOT_THEN_CLICK);
    await new Promise((r) => setTimeout(r, Number(process.env.SHOT_THEN_WAIT ?? 2000)));
  }

  await page.screenshot({ path: out, fullPage: true });
  console.log("wrote", out);
} finally {
  await browser.close();
}

/** Presses the first button whose visible text matches, the way a person would. */
async function clickByText(page, text) {
  const found = await page.evaluate((wanted) => {
    const button = [...document.querySelectorAll("button")].find(
      (b) => b.textContent?.trim() === wanted,
    );
    if (!button) return false;
    button.click();
    return true;
  }, text);
  if (!found) {
    throw new Error(`no button labelled ${JSON.stringify(text)}`);
  }
}
