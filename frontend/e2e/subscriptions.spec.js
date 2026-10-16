import { test, expect } from '@playwright/test';

test('добавление сохраняет подписку, ошибки сохраняют ввод', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('link', {name: 'Войти через Telegram'}).click();
  await page.getByRole('link', {name: 'Владелец', exact: true}).click();
  await page.getByRole('button', {name: 'Добавить подписку', exact: true}).click();
  const name = `Интернет ${test.info().project.name} ${Date.now()}`;
  await page.getByLabel('Название').fill(name);
  await page.getByLabel('Сумма').fill('799.001');
  await page.getByLabel('Валюта').selectOption('EUR');
  await page.getByLabel('Периодичность').selectOption('90');
  await page.getByRole('button', {name: 'Сохранить подписку'}).click();
  await expect(page.getByText('Введите положительную сумму с точностью валюты')).toBeVisible();
  await expect(page.getByLabel('Название')).toHaveValue(name);
  await page.getByLabel('Сумма').fill('799.01');
  let releaseResponse;
  const responseGate = new Promise(resolve => { releaseResponse = resolve; });
  let saves = 0;
  await page.route('**/api/subscriptions', async route => {
    if (route.request().method() !== 'POST') { await route.continue(); return; }
    saves++;
    const response = await route.fetch();
    await responseGate;
    await route.fulfill({response});
  });
  // Labelled inputs and keyboard submit remain usable without pointer actions.
  await page.getByRole('button', {name: 'Сохранить подписку'}).focus();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('button', {name:'Сохраняем…'})).toBeDisabled();
  await expect(page.getByRole('status').filter({hasText:'Подписка добавлена'})).toHaveCount(0);
  releaseResponse();
  await expect(page.getByRole('status').filter({hasText:'Подписка добавлена'})).toBeVisible();
  expect(saves).toBe(1);
  const item = page.getByRole('listitem').filter({hasText:name});
  await expect(item).toContainText('799.01 EUR');
  await expect(item).toContainText('90 дней');
  await page.reload();
  await expect(item).toBeVisible();
  await expect(item).toContainText('799.01 EUR');
  await expect(page.locator('body')).toHaveJSProperty('scrollWidth', await page.evaluate(() => document.body.clientWidth));
});
