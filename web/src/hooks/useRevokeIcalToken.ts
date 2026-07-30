import { useMutation } from '@tanstack/react-query';
import { revokeIcalToken } from '../api/ical';

export function useRevokeIcalToken() {
  return useMutation({ mutationFn: revokeIcalToken });
}
