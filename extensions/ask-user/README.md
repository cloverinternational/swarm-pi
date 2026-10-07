# Ask-user

Independent Trebol extension for interactive clarification, powered by the MIT-licensed
[eko24ive/pi-ask](https://github.com/eko24ive/pi-ask) implementation at
`49482b7e5d0d57be8af1db81f490f6e860792cfb`. See LICENSE for copyright and terms.

Loaded through `index.ts`, like the other Trebol extensions. The public tool remains
`ask_user_question`; its questionnaire schema, UI, replay and settings come from
pi-ask. See `docs/contract.md` and `docs/configuration.md`. No nested repository or submodule.

Validation: `npm run test:ask-user` and `npm run check:install -- --extension extensions/ask-user/index.ts`.
The former implementation’s auto-mode consultation and legacy single-question schema
are not retained; use the registered questionnaire schema.
