# Garry's Mod support

Add Garry's Mod to the existing Source egg, using the shared game registry and
existing SteamCMD, deployment, update, password and console paths.

- Panel name `GMod`, internal key `gmod`, directory `garrysmod`, download app
  `4020`, client/GSLT app `4000`, default map `gm_construct`.
- Keep the default `public` branch and 32-bit `srcds_run` launcher. Select
  `srcds_run_x64` and `bin/linux64/srcds` for the optional `x86-64` branch.
- Skip automatic Metamod/SourceMod installation for GMod; its Lua addons remain
  server-local. Preserve server-added Lua, gamemode and data paths outside the
  Steam inventory during managed updates, following existing delivery ownership.
  Shipped files still update normally; use addons for persistent overrides.
- Reuse `CUSTOM_PARAMS` for gamemode and native Workshop collection arguments;
  ensure gamemode arguments precede the startup map.
- Include GMod in the panel selector and node predownload choices, including
  `all`. Document setup and retain the requirement for live game acceptance.

Approach: extend the existing profile and shared runtime. A separate egg would
duplicate the node integration, while a selector-only edit would omit launcher
and framework handling. Existing game behavior stays covered by its tests.

Sources: [Facepunch dedicated-server setup](https://wiki.facepunch.com/gmod/Downloading_a_Dedicated_Server),
[GSLT app identity](https://wiki.facepunch.com/gmod/Steam_Game_Server_Accounts),
[x64 launcher and native binary](https://github.com/Facepunch/garrysmod-issues/issues/6665).
