import { expect, test } from '@playwright/test';
import {
  calculateRoutes,
  createDriverViaAPI,
  createLocationViaAPI,
  createParticipantViaAPI,
  uniqueName,
} from './helpers';

test('session restore flow: calculated routes persist after leaving and returning to home', async ({ page, request }) => {
  const locationName = uniqueName('Session Location');
  const participantName = uniqueName('Session Participant');
  const driverName = uniqueName('Session Driver');

  const locationAddress = '100 Market St, San Francisco, CA 94105';
  const participantAddress = '500 Howard St, San Francisco, CA 94105';
  const driverAddress = '1 Dr Carlton B Goodlett Pl, San Francisco, CA 94102';

  await createLocationViaAPI(request, locationName, locationAddress);
  await createParticipantViaAPI(request, participantName, participantAddress);
  await createDriverViaAPI(request, driverName, driverAddress, 4);

  const locationLabel = `${locationName} (${locationAddress})`;
  await calculateRoutes(page, locationLabel, participantName, driverName);

  await page.getByRole('link', { name: 'Drivers' }).click();
  await expect(page).toHaveURL(/\/drivers$/);

  await page.getByRole('link', { name: 'Plan Event' }).click();
  await expect(page).toHaveURL(/\/$/);

  const results = page.getByTestId('results-section');
  await expect(results.getByTestId('calculated-routes')).toBeVisible();
  await expect(results).toContainText('Calculated Routes');
  await expect(results).toContainText(participantName);
  await expect(results).toContainText(driverName);
});
