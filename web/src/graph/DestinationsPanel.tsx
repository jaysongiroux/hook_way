import { Text } from "@mantine/core";
import { DestinationEditor } from "./DestinationEditor";
import { DestinationTypePicker } from "./DestinationTypePicker";
import { NamedConfigPanel } from "./NamedConfigPanel";
import { destSchema } from "./nodeSchema";
import { useGraphStore } from "./store";

export function DestinationsPanel({ opened, onClose }: { opened: boolean; onClose: () => void }) {
  const destinations = useGraphStore((s) => s.destinations);
  const setDestinations = useGraphStore((s) => s.setDestinations);
  return (
    <NamedConfigPanel
      opened={opened}
      onClose={onClose}
      title="Destinations"
      addLabel="Add destination"
      entries={destinations}
      onChange={setDestinations}
      defaultType="webhook"
      createEntry={(type) => ({ type, [destSchema(type)?.block ?? type]: {} })}
      Picker={DestinationTypePicker}
      Editor={DestinationEditor}
      description={
        <>
          Shared targets referenced by <Text span ff="monospace">publish</Text> nodes. This panel generates config; it does
          not talk to a running Hookway process.
        </>
      }
    />
  );
}
