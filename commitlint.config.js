module.exports = {
	extends: ['@commitlint/config-conventional'],
	// Ignore automated merge/PR messages that don't follow Conventional Commits
	ignores: [
		(commit) => commit.startsWith('Merge branch'),
		(commit) => commit.startsWith('Merge pull request'),
		(commit) => /\(#\d+\)$/.test(commit),
	],
};