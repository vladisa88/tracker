import { test, expect } from '@playwright/test';

test('владелец входит, перезагружает страницу и выходит', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('link', {name: 'Войти через Telegram'}).click();
  await page.getByRole('link', {name: 'Владелец', exact: true}).click();
  await expect(page.getByRole('heading', {name: 'Ближайшие платежи'})).toBeVisible();
  await page.reload();
  await expect(page.getByRole('button', {name: 'Выйти'})).toBeVisible();
  await expect(page.locator('body')).toHaveJSProperty('scrollWidth', await page.evaluate(() => document.body.clientWidth));
  await page.getByRole('button', {name: 'Выйти'}).click();
  await expect(page.getByRole('link', {name: 'Войти через Telegram'})).toBeVisible();
  expect((await page.request.get('/api/session')).status()).toBe(401);
});

test('посторонний аккаунт не получает доступ', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('link', {name: 'Войти через Telegram'}).click();
  await page.getByRole('link', {name: 'Посторонний'}).click();
  await expect(page.getByRole('alert')).toContainText('Доступ разрешён только владельцу');
  expect((await page.request.get('/api/session')).status()).toBe(401);
  await expect(page.getByRole('button', {name: 'Выйти'})).toHaveCount(0);
});
