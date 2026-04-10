const { chromium } = require('playwright');
const crypto = require('crypto');
const fs = require('fs');
const path = require('path');

const log = (msg: string) => process.stderr.write(`[playwright] ${msg}\n`);

const LOGIN_HOSTS = ['login.microsoftonline.com', 'login.microsoft.com', 'login.live.com', 'signin.aws.amazon.com', 'accounts.google.com', 'login.windows.net'];

async function boot() {
  const outputFile = process.env.PLAYWRIGHT_OUTPUT_FILE!;
  const userDataDir = process.env.PLAYWRIGHT_USER_DATA_DIR!;
  const screenshotsDir = process.env.PLAYWRIGHT_SCREENSHOTS_DIR || '/tmp/screenshots';
  const queryDir = process.env.PLAYWRIGHT_QUERY_DIR || '/tmp/query';
  const headless = process.env.HEADLESS !== 'false';
  const timeout = parseInt(process.env.TIMEOUT || '300', 10);

  log(`initializing: headless=${headless} timeout=${timeout}s`);
  log(`  userDataDir: ${userDataDir}`);
  log(`  screenshotsDir: ${screenshotsDir}`);
  log(`  queryDir: ${queryDir}`);
  log(`  outputFile: ${outputFile}`);

  const launchOpts: any = {
    headless,
    viewport: { width: 1920, height: 1080 },
  };

  if (process.env.PLAYWRIGHT_HAR_FILE) {
    launchOpts.recordHar = { path: process.env.PLAYWRIGHT_HAR_FILE };
    log(`HAR recording: ${process.env.PLAYWRIGHT_HAR_FILE}`);
  }

  if (process.env.PLAYWRIGHT_VIDEO_DIR) {
    launchOpts.recordVideo = { dir: process.env.PLAYWRIGHT_VIDEO_DIR };
    log(`video recording: ${process.env.PLAYWRIGHT_VIDEO_DIR}`);
  }

  // Cookies and per-origin localStorage are applied manually after launch via
  // addCookies / addInitScript. Passing them through launchPersistentContext's
  // storageState option does not get them into the browser jar.
  let pendingStorageState: { cookies?: any[]; origins?: any[] } | null = null;
  if (process.env.PLAYWRIGHT_STORAGE_STATE) {
    try {
      pendingStorageState = JSON.parse(fs.readFileSync(process.env.PLAYWRIGHT_STORAGE_STATE, 'utf-8'));
      log(`storage state loaded from ${process.env.PLAYWRIGHT_STORAGE_STATE}`);
    } catch (e: any) {
      log(`failed to read storage state: ${e.message || e}`);
    }
  }

  const browser = await chromium.launchPersistentContext(userDataDir, launchOpts);

  if (pendingStorageState && Array.isArray(pendingStorageState.cookies) && pendingStorageState.cookies.length > 0) {
    try {
      await browser.addCookies(pendingStorageState.cookies);
      log(`injected ${pendingStorageState.cookies.length} cookies into browser context`);
    } catch (e: any) {
      log(`failed to inject cookies: ${e.message || e}`);
    }
  }

  if (pendingStorageState && Array.isArray(pendingStorageState.origins)) {
    for (const origin of pendingStorageState.origins) {
      if (!origin || !origin.origin || !Array.isArray(origin.localStorage) || origin.localStorage.length === 0) continue;
      const data = { origin: origin.origin, items: origin.localStorage as Array<{ name: string; value: string }> };
      await browser.addInitScript((d: { origin: string; items: Array<{ name: string; value: string }> }) => {
        try {
          if (window.location.origin === d.origin) {
            for (const it of d.items) {
              try { localStorage.setItem(it.name, it.value); } catch {}
            }
          }
        } catch {}
      }, data);
      log(`localStorage init script registered for ${origin.origin} (${origin.localStorage.length} items)`);
    }
  }

  if (process.env.PLAYWRIGHT_SESSION_STORAGE_FILE) {
    try {
      const raw = fs.readFileSync(process.env.PLAYWRIGHT_SESSION_STORAGE_FILE, 'utf-8');
      const payload = JSON.parse(raw) as { origin: string; items: Record<string, string> };
      if (payload && payload.origin && payload.items) {
        await browser.addInitScript((data: { origin: string; items: Record<string, string> }) => {
          try {
            if (window.location.origin === data.origin) {
              for (const k of Object.keys(data.items)) {
                try { sessionStorage.setItem(k, data.items[k]); } catch {}
              }
            }
          } catch {}
        }, payload);
        log(`sessionStorage init script registered for ${payload.origin} (${Object.keys(payload.items).length} items)`);
      }
    } catch (e: any) {
      log(`failed to register sessionStorage init script: ${e.message || e}`);
    }
  }

  const page = browser.pages()[0] || await browser.newPage();

  log(`browser launched, page url=${page.url()}`);

  // Handle BROWSER_LOGIN_URL — navigate to federation login URL if provided
  if (process.env.BROWSER_LOGIN_URL) {
    log('navigating to login URL...');
    await page.goto(process.env.BROWSER_LOGIN_URL, { waitUntil: 'domcontentloaded' });
    // Wait for AWS console to fully load (account menu appears after all redirects)
    await page.waitForSelector('#nav-usernameMenu', { timeout: 30000 }).catch(() => {
      log('account menu not found, waiting for page to settle...');
    });
    await page.waitForTimeout(2000);
    const loginUrl = page.url();
    log(`login complete, url=${loginUrl}`);
  }

  const changes: any[] = [];

  const cleanupPage = async () => {
    await page.evaluate(() => {
      // AWS console sidebar
      const awsNav = document.querySelector('[aria-label="Close side navigation"]') as HTMLElement;
      if (awsNav) awsNav.click();
      // AWS console footer
      document.getElementById('console-nav-footer-inner')?.remove();
      // Azure portal sidebar
      const azureNav = document.querySelector('[data-telemetryname="SideBar"] button[aria-label*="collapse"], .fxs-sidebar-collapsed, #sidebar button[aria-expanded="true"]') as HTMLElement;
      if (azureNav) azureNav.click();
    }).catch(() => {});
  };

  const screenshot = async (name: string, opts?: { fullPage?: boolean; watermark?: string }): Promise<string> => {
    await page.waitForLoadState('domcontentloaded').catch(() => {});
    await page.waitForTimeout(1000);
    await cleanupPage();

    // Fixed-position SPAs (Azure portal, etc.) pin the document body to the
    // viewport — body.scrollHeight never grows, so fullPage:true alone captures
    // only the viewport. Stretching the viewport forces the SPA to lay out its
    // inner overflow:auto panels taller, after which fullPage:true captures the
    // expanded result. The viewport is restored before the helper returns.
    const wantsFullPage = opts?.fullPage !== false;
    let originalViewport: { width: number; height: number } | null = null;
    if (wantsFullPage) {
      originalViewport = page.viewportSize();
      if (originalViewport) {
        await page.setViewportSize({ width: originalViewport.width, height: 1000 });
        await page.waitForTimeout(500);
      }
    }

    if (opts?.watermark) {
      const currentUrl = page.url();
      const lines = [
        new Date().toISOString(),
        name,
        opts.watermark,
        currentUrl,
      ];
      await page.evaluate((lines: string[]) => {
        const el = document.createElement('div');
        el.id = '__playwright_watermark';
        el.style.cssText = `
          position:fixed;bottom:8px;right:8px;
          z-index:999999;pointer-events:none;
          font:14px/1.6 monospace;
          color:rgba(0,0,0,0.4);
          text-align:right;
          text-shadow:0 0 2px rgba(255,255,255,0.6);
        `;
        el.innerHTML = lines.map(l => `<div>${l}</div>`).join('');
        document.body.appendChild(el);
      }, lines);
    }

    const safeName = name.replace(/[^a-zA-Z0-9_-]/g, '_');
    const screenshotPath = path.join(screenshotsDir, `${safeName}.png`);
    await page.screenshot({ fullPage: opts?.fullPage ?? true, path: screenshotPath });

    if (opts?.watermark) {
      await page.evaluate(() => document.getElementById('__playwright_watermark')?.remove()).catch(() => {});
    }

    if (wantsFullPage && originalViewport) {
      await page.setViewportSize(originalViewport);
    }

    log(`screenshot: ${screenshotPath}`);
    return screenshotPath;
  };

  const appendChange = (change: {
    change_type: string;
    config_id?: string;
    id?: string;             // alias for external_change_id
    external_id?: string;
    external_change_id?: string;
    config_type?: string;
    summary?: string;
    severity?: string;
    source?: string;
    details?: Record<string, any>;
    screenshot?: string;
  }) => {
    if (change.id && !change.external_change_id) {
      change.external_change_id = change.id;
      delete (change as any).id;
    }
    if (change.screenshot) {
      const stat = fs.statSync(change.screenshot);
      const sha = crypto.createHash('sha256').update(fs.readFileSync(change.screenshot)).digest('hex');
      const existing = change.details?.artifacts || [];
      change.details = {
        ...change.details,
        artifacts: [...existing, {
          id: crypto.randomUUID(),
          path: change.screenshot,
          name: path.basename(change.screenshot),
          sha,
          size: stat.size,
        }],
      };
    }
    changes.push(change);
    log(`change: ${change.change_type} ${change.config_id || change.external_id || ''}`);
  };

  const writeOutput = (data: any) => {
    fs.writeFileSync(outputFile, JSON.stringify({ data, changes }));
    log(`output: ${Array.isArray(data) ? data.length + ' items' : 'object'}, ${changes.length} changes`);
  };

  const checkLogin = async (url?: string) => {
    if (url) {
      await page.goto(url, { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(3000);
    }

    let currentHost = '';
    try { currentHost = new URL(page.url()).hostname.toLowerCase(); } catch {}
    if (LOGIN_HOSTS.includes(currentHost)) {
      await screenshot('login_failure');
      throw new Error(`Login failed: redirected to ${page.url()}`);
    }

    const title = await page.title();
    if (/error|denied|unauthorized|forbidden|expired/i.test(title)) {
      await screenshot('login_failure');
      throw new Error(`Login failed: page title indicates error: ${title}`);
    }

    log(`authenticated: url=${page.url()}`);
  };

  const close = async (opts?: { error?: boolean }) => {
    // Rename video file for clarity
    const videoDir = process.env.PLAYWRIGHT_VIDEO_DIR;
    const videoPage = page.video();
    await browser.close();
    if (videoDir && videoPage) {
      try {
        const videoPath = await videoPage.path();
        if (videoPath && fs.existsSync(videoPath)) {
          const destName = opts?.error ? 'error.webm' : 'script.webm';
          const dest = path.join(videoDir, destName);
          fs.renameSync(videoPath, dest);
          log(`video saved: ${dest}`);
        }
      } catch {}
    }
    log('browser closed');
  };

  return {
    browser,
    page,
    screenshotsDir,
    queryDir,
    outputFile,
    log,
    screenshot,
    captureScreenshot: screenshot, // alias
    appendChange,
    writeOutput,
    checkLogin,
    close,
  };
}

module.exports = { boot };
