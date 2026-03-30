import { APIRequestContext, expect, Page } from '@playwright/test';

export function uniqueName(prefix: string): string {
  return `${prefix}-${Date.now()}-${Math.floor(Math.random() * 10000)}`;
}

export async function createLocationViaAPI(request: APIRequestContext, name: string, address: string) {
  const response = await request.post('/api/v1/activity-locations', {
    data: { name, address },
  });
  expect(response.ok()).toBeTruthy();
  return response.json();
}

export async function createParticipantViaAPI(request: APIRequestContext, name: string, address: string) {
  const response = await request.post('/api/v1/participants', {
    data: { name, address },
  });
  expect(response.ok()).toBeTruthy();
  return response.json();
}

export async function createDriverViaAPI(request: APIRequestContext, name: string, address: string, vehicleCapacity: number) {
  const response = await request.post('/api/v1/drivers', {
    data: { name, address, vehicle_capacity: vehicleCapacity },
  });
  expect(response.ok()).toBeTruthy();
  return response.json();
}

export async function calculateRoutes(page: Page, locationLabel: string, participantName: string, driverName: string) {
  await page.goto('/');
  await page.selectOption('#activity-location-native', { label: locationLabel });
  await page.getByRole('checkbox', { name: new RegExp(participantName, 'i') }).check();
  await page.getByRole('checkbox', { name: new RegExp(driverName, 'i') }).check();
  await page.locator('#route-time').fill('18:00');
  await page.getByTestId('calculate-routes-btn').click();
  await expect(page.getByTestId('results-section').getByTestId('calculated-routes')).toBeVisible();
}
