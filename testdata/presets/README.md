Preset fixtures for tests live in this directory.

`edge_cases.json` pins every model parameter to an end of its range. Its base
note is 36 rather than 127, and that is a constraint of the format rather than
an arbitrary choice: a preset has to stay buildable at every note the keyboard
can send, and transposing down multiplies every decay by 2^((base-36)/12). At
base note 127 that factor is 191.8, so the 500 ms decay this fixture carries
would reach 95891 ms at the bottom key and the preset would describe an
instrument whose low register cannot sound. Note 36 is the bottom of the
playable range, where the factor is 1 and every parameter extreme in the file
is simultaneously reachable. See model.ValidateAuthoredBarParams.
