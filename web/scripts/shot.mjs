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
 *
 * SHOT_STEPS is a JSON list of interactions to perform before the shot, and
 * SHOT_WAIT how long to settle afterwards.
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
    // Waits for the signed-in chrome to appear rather than for the sign-in form
    // to disappear. Absence was the wrong signal: the first screen behind the
    // form has a submit button of its own, so "no submit button" was never true
    // and every run timed out.
    await Promise.all([
      page.click('button[type="submit"]'),
      page.waitForFunction(
        () => [...document.querySelectorAll("button")].some((b) => b.textContent?.trim() === "Sign out"),
        { timeout: 15_000 },
      ),
    ]);
  }

  // Interactions, as a list, because a screenshot is often of a state that only
  // exists after a few of them:
  //
  //   SHOT_STEPS='[{"click":"Add a printer"},{"wait":8000},{"type":["input#x","hello"]}]'
  //
  // Steps run in order. A step that cannot find what it names fails loudly
  // rather than screenshotting the wrong thing quietly, which is the whole
  // reason for looking at all.
  for (const step of JSON.parse(process.env.SHOT_STEPS ?? "[]")) {
    if (step.wait !== undefined) {
      await new Promise((r) => setTimeout(r, step.wait));
    }
    if (step.click !== undefined) {
      await clickByText(page, step.click);
    }
    if (step.type !== undefined) {
      const [selector, value] = step.type;
      await page.waitForSelector(selector, { timeout: 10_000 });
      await page.type(selector, value);
    }
    if (step.select !== undefined) {
      const [selector, value] = step.select;
      await page.waitForSelector(selector, { timeout: 10_000 });
      await page.select(selector, value);
    }
    if (step.upload !== undefined) {
      const [selector, file] = step.upload;
      await page.waitForSelector(selector, { timeout: 10_000 });
      const input = await page.$(selector);
      await input.uploadFile(file);
    }
    if (step.press !== undefined) {
      await page.waitForSelector(step.press, { timeout: 10_000 });
      await page.click(step.press);
    }
  }

  await new Promise((r) => setTimeout(r, Number(process.env.SHOT_WAIT ?? 1000)));

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
