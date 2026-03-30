import { expect, test } from '@playwright/test';
import { uniqueName } from './helpers';

test('participant flow: create participant', async ({ page }) => {
  const participantName = uniqueName('Alice Rider');
  const address = '500 Howard St, San Francisco, CA 94105';

  await page.goto('/participants');
  await page.getByTestId('add-participant-open-btn').click();

  const form = page.getByTestId('participant-form-container');
  await form.getByLabel('Name').fill(participantName);
  await form.getByLabel('Address').fill(address);
  await form.getByRole('button', { name: 'Add Participant' }).click();

  await expect(page.getByTestId('participants-list')).toContainText(participantName);
  await expect(page.getByTestId('participants-list')).toContainText(address);
});
