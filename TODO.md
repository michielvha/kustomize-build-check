# Potential Improvements

- Add workflow that automatically updates the SHA in the action repository on each new release of the container
- Impact analysis only follows `resources`, `bases` and `components`. A changed
  file that a kustomization pulls in through `patches`, `configMapGenerator` or
  `secretGenerator` does not mark that kustomization as affected, so editing one
  can pass without being validated. `discovery.ParseKustomization` would need to
  parse those fields too.
