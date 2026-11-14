/**
 * Errors the RanobeLib module raises.
 *
 * A distinct class keeps callers from having to inspect message text, and the code stays stable
 * when the wording changes.
 */
export class RanobeLibError extends Error {
  constructor(message, { code = 'error', status = null, url = null, retryAfter = null, cause = null } = {}) {
    super(message, cause ? { cause } : undefined);
    this.name = 'RanobeLibError';
    this.code = code;
    this.status = status;
    this.url = url;
    this.retryAfter = retryAfter;
  }
}
