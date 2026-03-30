import { expect, test } from '@playwright/test';
import {
  calculateRoutes,
  createDriverViaAPI,
  createLocationViaAPI,
  createParticipantViaAPI,
  uniqueName,
} from './helpers';

test('route calculation flow: calculate routes from selected location/participant/driver', async ({ page, request }) => {
  const locationName = uniqueName('Route Location');
  const participantName = uniqueName('Route Participant');
  const driverName = uniqueName('Route Driver');

  const locationAddress = '100 Market St, San Francisco, CA 94105';
  const participantAddress = '500 Howard St, San Francisco, CA 94105';
  const driverAddress = '1 Dr Carlton B Goodlett Pl, San Francisco, CA 94102';

  await createLocationViaAPI(request, locationName, locationAddress);
  await createParticipantViaAPI(request, participantName, participantAddress);
  await createDriverViaAPI(request, driverName, driverAddress, 4);

  const locationLabel = `${locationName} (${locationAddress})`;
  await calculateRoutes(page, locationLabel, participantName, driverName);

  const results = page.getByTestId('results-section');
  await expect(results).toContainText('Calculated Routes');
  await expect(results).toContainText(participantName);
  await expect(results).toContainText(driverName);
});
