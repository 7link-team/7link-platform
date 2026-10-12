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
