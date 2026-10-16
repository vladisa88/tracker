import {test, expect} from '@playwright/test';

test('прогноз обновляется после добавления, изменения, архива и восстановления', async ({page}) => {
  await page.goto('/');
  await page.getByRole('link', {name:'Войти через Telegram'}).click();
  await page.getByRole('link', {name:'Владелец', exact:true}).click();
  const upcoming = page.getByRole('region', {name:'Платежи в ближайшие 30 дней', exact:true});
  const average = page.getByRole('region', {name:'В среднем за 30 дней', exact:true});
  await expect(upcoming).toBeVisible();
  await expect(average).toBeVisible();
  await expect(page.getByText('Загружаем итоги…')).toHaveCount(0);
  // Read the displayed baseline: other end-to-end scenarios share the test database.
  const baseline = async region => {
    const row = region.locator('dd').filter({hasText:'RUB'});
    return await row.count() ? (await row.innerText()).replace(' RUB','') : '0.00';
  };
  const initialUpcoming = await baseline(upcoming);
  const initialAverage = await baseline(average);
  const initialRows = await upcoming.locator('dd').filter({hasText:'RUB'}).count();
  const plus = (value, cents) => {
    const total = BigInt(value.replace('.','')) + BigInt(cents);
    return `${total / 100n}.${String(total % 100n).padStart(2,'0')} RUB`;
  };
  await page.getByRole('button', {name:'Добавить подписку', exact:true}).click();
  const name = `Годовой ${test.info().project.name} ${Date.now()}`;
  await page.getByLabel('Название').fill(name);
  await page.getByLabel('Сумма').fill('365');
  await page.getByLabel('Периодичность').selectOption('365');
  await page.getByRole('button', {name:'Сохранить подписку'}).click();
  await expect(upcoming.locator('dd').filter({hasText:'RUB'})).toHaveText(plus(initialUpcoming, 36500));
  await expect(average.locator('dd').filter({hasText:'RUB'})).toHaveText(plus(initialAverage, 3000));
  const item = page.getByRole('listitem').filter({hasText:name});
  await item.getByRole('button', {name:'Редактировать', exact:true}).click();
  await page.getByLabel('Сумма').fill('730');
  await page.getByRole('button', {name:'Сохранить изменения'}).click();
  await expect(upcoming.locator('dd').filter({hasText:'RUB'})).toHaveText(plus(initialUpcoming, 73000));
  await expect(average.locator('dd').filter({hasText:'RUB'})).toHaveText(plus(initialAverage, 6000));
  await item.getByRole('button', {name:'В архив', exact:true}).click();
  if (initialRows) {
    await expect(upcoming.locator('dd').filter({hasText:'RUB'})).toHaveText(`${initialUpcoming} RUB`);
    await expect(average.locator('dd').filter({hasText:'RUB'})).toHaveText(`${initialAverage} RUB`);
  } else {
    await expect(upcoming.locator('dd').filter({hasText:'RUB'})).toHaveCount(0);
    await expect(average.locator('dd').filter({hasText:'RUB'})).toHaveCount(0);
  }
  await page.getByRole('button', {name:'Архив', exact:true}).click();
  await item.getByRole('button', {name:'Восстановить', exact:true}).click();
  await page.getByLabel('Следующая дата платежа').fill('2099-12-31');
  await page.getByRole('button', {name:'Восстановить подписку', exact:true}).click();
  await expect(average.locator('dd').filter({hasText:'RUB'})).toHaveText(plus(initialAverage, 6000));
  await expect(upcoming.locator('dd').filter({hasText:'RUB'})).toHaveText(`${initialUpcoming} RUB`);
  await expect(page.locator('body')).toHaveJSProperty('scrollWidth', await page.evaluate(() => document.body.clientWidth));
});

test('итоги показывают загрузку, ошибку и восстанавливаются при повторе', async ({page}) => {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  await page.route('**/api/summary', async route => {
    await gate;
    await route.fulfill({status:500, contentType:'application/json', body:'{}'});
  });
  await page.goto('/');
  await page.getByRole('link', {name:'Войти через Telegram'}).click();
  await page.getByRole('link', {name:'Владелец', exact:true}).click();
  await expect(page.getByText('Загружаем итоги…')).toBeVisible();
  release();
  await expect(page.getByRole('alert').filter({hasText:'Не удалось загрузить итоги'})).toBeVisible();
  await expect(page.getByRole('button', {name:'Добавить подписку', exact:true})).toBeEnabled();
  await page.unroute('**/api/summary');
  await page.getByRole('button', {name:'Повторить загрузку итогов'}).click();
  await expect(page.getByRole('region', {name:'Платежи в ближайшие 30 дней', exact:true})).toBeVisible();
  await expect(page.getByRole('region', {name:'В среднем за 30 дней', exact:true})).toBeVisible();
  await page.screenshot({path:test.info().outputPath('forecast.png'), fullPage:true});
});
