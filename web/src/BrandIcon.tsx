import { Box } from "@mantine/core";

type BrandIconProps = {
  size?: number;
};

export function BrandIcon({ size = 28 }: BrandIconProps) {
  return (
    <Box
      component="img"
      src="/hookway-icon-512.png"
      alt=""
      aria-hidden="true"
      w={size}
      h={size}
      style={{ display: "block", flexShrink: 0 }}
    />
  );
}
