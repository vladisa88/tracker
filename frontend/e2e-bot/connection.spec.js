import { test, expect } from '@playwright/test';

test('отдельное подключение бота, ошибка и повторная проверка', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('link', {name: 'Войти через Telegram'}).click();
  await page.getByRole('link', {name: 'Владелец', exact: true}).click();
  const panel = page.getByRole('region', {name: 'Подключите бота'});
  await expect(panel).toContainText('Вход через Telegram не подключает напоминания');
  await expect(panel.getByRole('link', {name: 'Открыть бота'})).toHaveAttribute('href', 'https://t.me/tracking_test_bot');
  await expect(panel).toContainText('/start');
  await page.reload();
  await expect(page.getByRole('heading', {name: 'Подключите бота'})).toBeVisible();
  expect((await page.request.get('/test-bot')).status()).toBe(204);
  await page.getByRole('button', {name: 'Проверить подключение'}).click();
  await expect(page.getByRole('heading', {name: 'Бот подключён'})).toBeVisible();
  await page.reload();
  await expect(page.getByRole('region', {name: 'Бот подключён'})).toContainText('Проверочное сообщение отправлено');
  expect((await page.request.get('/test-bot?mode=error')).status()).toBe(204);
  await page.getByRole('button', {name: 'Проверить подключение'}).click();
  await expect(page.getByRole('region', {name: 'Бот недоступен'})).toContainText('Разблокируйте бота');
  await expect(page.getByRole('button', {name: 'Выйти'})).toBeEnabled();
  expect((await page.request.get('/test-bot')).status()).toBe(204);
  await page.getByRole('button', {name: 'Проверить подключение'}).click();
  await expect(page.getByRole('heading', {name: 'Бот подключён'})).toBeVisible();
  await expect(page.locator('body')).toHaveJSProperty('scrollWidth', await page.evaluate(() => document.body.clientWidth));
});


test('ошибка ежедневной отправки видна и не мешает учёту; /start восстанавливает связь', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('link', {name: 'Войти через Telegram'}).click();
  await page.getByRole('link', {name: 'Владелец', exact: true}).click();
  expect((await page.request.get('/test-bot')).status()).toBe(204);
  await page.getByRole('button', {name: 'Проверить подключение'}).click();
  await expect(page.getByRole('heading', {name: 'Бот подключён'})).toBeVisible();
  await page.getByRole('button', {name: 'Добавить подписку', exact: true}).click();
  const name = `Платёж ${test.info().project.name} ${Date.now()}`;
  await page.getByLabel('Название').fill(name);
  await page.getByLabel('Сумма').fill('990');
  await page.getByRole('button', {name: 'Сохранить подписку'}).click();
  await expect(page.getByRole('listitem').filter({hasText: name})).toBeVisible();
  expect((await page.request.get('/test-digest')).status()).toBe(204);
  await page.reload();
  await expect(page.getByRole('region', {name: 'Бот недоступен'})).toContainText('Разблокируйте бота');
  const item = page.getByRole('listitem').filter({hasText: name});
  await item.getByRole('button', {name: 'Редактировать', exact: true}).click();
  await page.getByLabel('Сумма').fill('1000');
  await page.getByRole('button', {name: 'Сохранить изменения'}).click();
  await expect(item).toContainText('1000.00 RUB');
  expect((await page.request.get('/test-bot')).status()).toBe(204);
  await page.getByRole('button', {name: 'Проверить подключение'}).click();
  await expect(page.getByRole('heading', {name: 'Бот подключён'})).toBeVisible();
});
