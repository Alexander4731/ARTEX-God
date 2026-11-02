const path = require('node:path');
const root = process.argv[2];
const puppeteer = require(path.join(root, 'skills', 'api-recon', 'scripts', 'node_modules', 'puppeteer-core'));

(async () => {
  const browser = await puppeteer.launch({
    executablePath: path.join(root, 'runtime', 'chrome', 'chrome.exe'),
    headless: true,
  });
  try {
    const page = await browser.newPage();
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    const response = await page.goto('http://127.0.0.1:8787/setup/', {waitUntil: 'networkidle0'});
    await page.waitForSelector('input[type="password"]');
    if (response.status() !== 200 || errors.length) {
      throw new Error(`Setup page failed: ${response.status()} ${errors.join('; ')}`);
    }
    console.log(JSON.stringify({passed: true, version: await browser.version(), title: await page.title()}));
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error.message); process.exit(1); });
