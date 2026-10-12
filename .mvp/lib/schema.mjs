export const planSchema = {
  type: 'object', additionalProperties: false, required: ['workItems'],
  properties: { workItems: { type: 'array', minItems: 1, items: {
    type: 'object', additionalProperties: false,
    required: ['id', 'title', 'description', 'acceptanceRefs', 'files'],
    properties: {
      id: { type: 'string', pattern: '^W[1-9][0-9]*$' }, title: { type: 'string' }, description: { type: 'string' },
      acceptanceRefs: { type: 'array', items: { type: 'string' } }, files: { type: 'array', items: { type: 'string' } },
    },
  } } },
};

const finding = { type: 'object', additionalProperties: false, required: ['severity', 'title', 'evidence', 'expected'],
  properties: { severity: { type: 'string', enum: ['BLOCKER', 'MAJOR', 'MINOR'] },
    title: { type: 'string' }, evidence: { type: 'string' }, expected: { type: 'string' } } };
export const testSchema = { type: 'object', additionalProperties: false, required: ['pass', 'findings'],
  properties: { pass: { type: 'boolean' }, findings: { type: 'array', items: finding } } };
export const acceptanceSchema = { type: 'object', additionalProperties: false, required: ['pass', 'criteria', 'uxFindings'],
  properties: { pass: { type: 'boolean' }, criteria: { type: 'array', items: {
    type: 'object', additionalProperties: false, required: ['ref', 'verdict', 'evidence'],
    properties: { ref: { type: 'string' }, verdict: { type: 'string', enum: ['PASS', 'FAIL', 'UNVERIFIED'] }, evidence: { type: 'string' } },
  } }, uxFindings: { type: 'array', items: finding } } };
