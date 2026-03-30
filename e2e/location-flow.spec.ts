import { expect, test } from '@playwright/test';
import { uniqueName } from './helpers';

test('location flow: create activity location', async ({ page }) => {
  const locationName = uniqueName('Mosque');
  const address = '100 Market St, San Francisco, CA 94105';

  await page.goto('/activity-locations');
  await page.getByLabel('Location Name').fill(locationName);
  await page.getByLabel('Address').fill(address);
  await page.getByTestId('add-location-btn').click();

  const locationList = page.getByTestId('location-list');
  await expect(locationList).toContainText(locationName);
  await expect(locationList).toContainText(address);
});
