import { expect, test } from '@playwright/test';
import { uniqueName } from './helpers';

test('driver flow: create driver', async ({ page }) => {
  const driverName = uniqueName('Bob Driver');
  const address = '1 Dr Carlton B Goodlett Pl, San Francisco, CA 94102';

  await page.goto('/drivers');
  await page.getByTestId('add-driver-open-btn').click();

  const form = page.getByTestId('driver-form-container');
  await form.getByLabel('Name').fill(driverName);
  await form.getByLabel('Address').fill(address);
  await form.getByLabel('Available Vehicle Capacity').fill('4');
  await form.getByRole('button', { name: 'Add Driver' }).click();

  await expect(page.getByTestId('drivers-list')).toContainText(driverName);
  await expect(page.getByTestId('drivers-list')).toContainText(address);
});
