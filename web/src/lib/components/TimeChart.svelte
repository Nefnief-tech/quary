<script lang="ts">
	// TimeChart draws a fluid area+line chart over time series, sized to its
	// container via viewBox stretching. Values are 0..max (percent-like);
	// passing max: 0 switches to auto-scaling off the data.
	let {
		points = [], // { t: number; values: number[] }
		series = [], // { color: string; label?: string; fill?: boolean }
		height = 40,
		max = 100
	}: {
		points?: { t: number; values: number[] }[];
		series?: { color: string; label?: string; fill?: boolean }[];
		height?: number;
		max?: number;
	} = $props();

	const W = 300;

	const effectiveMax = $derived(
		max > 0
			? max
			: Math.max(1, ...points.flatMap((p) => p.values.map((v) => Math.ceil(v))))
	);

	// catmull-rom -> cubic bezier keeps the lines fluid without overshoot.
	function linePath(values: number[]): string {
		const n = points.length;
		if (n === 0) return '';
		const xs = points.map((_, i) => (n === 1 ? W : (i / (n - 1)) * W));
		const ys = values.map((v) => height - 2 - (clamp(v) / effectiveMax) * (height - 4));
		if (n === 1) return `M ${xs[0]} ${ys[0]}`;
		let d = `M ${xs[0]} ${ys[0]}`;
		for (let i = 0; i < n - 1; i++) {
			const p0 = points[i - 1] ? ys[i - 1] : ys[i];
			const p3 = points[i + 2] ? ys[i + 2] : ys[i + 1];
			const c1x = xs[i] + (xs[i + 1] - xs[i]) / 3;
			const c2x = xs[i + 1] - (xs[i + 1] - xs[i]) / 3;
			d += ` C ${c1x} ${smooth(p0, ys[i])}, ${c2x} ${smooth(p3, ys[i + 1])}, ${xs[i + 1]} ${ys[i + 1]}`;
		}
		return d;
	}

	// soften control points so sharp spikes read as curves, not cliffs
	function smooth(target: number, from: number): number {
		return from + (target - from) / 2;
	}

	function clamp(v: number): number {
		return Math.max(0, Math.min(v, effectiveMax));
	}

	function areaPath(values: number[]): string {
		if (points.length < 2) return '';
		return `${linePath(values)} L ${W} ${height} L 0 ${height} Z`;
	}

	const paths = $derived(
		series.map((s, i) => ({
			...s,
			line: linePath(points.map((p) => p.values[i] ?? 0)),
			area: areaPath(points.map((p) => p.values[i] ?? 0))
		}))
	);

	const gid = Math.random().toString(36).slice(2, 8);
</script>

<svg
	viewBox="0 0 {W} {height}"
	preserveAspectRatio="none"
	class="block w-full overflow-visible"
	role="img"
	aria-label={series.map((s) => s.label).join(', ')}
>
	<defs>
		{#each paths as s, i (i)}
			<linearGradient id="{gid}-g{i}" x1="0" y1="0" x2="0" y2="1">
				<stop offset="0%" stop-color={s.color} stop-opacity="0.28" />
				<stop offset="100%" stop-color={s.color} stop-opacity="0" />
			</linearGradient>
		{/each}
	</defs>
	{#if points.length < 2}
		<line x1="0" y1={height - 2} x2={W} y2={height - 2} stroke="currentColor" stroke-opacity="0.15" stroke-width="1" />
	{:else}
		{#each paths as s, i (i)}
			{#if s.fill !== false}
				<path d={s.area} fill="url(#{gid}-g{i})" />
			{/if}
			<path d={s.line} fill="none" stroke={s.color} stroke-width="1.6" vector-effect="non-scaling-stroke" />
		{/each}
	{/if}
</svg>
