import { useState } from 'react';

import type { types } from '../../../../wailsjs/go/models';

export default function NewSiteModal({ addSite }: { addSite: (site: types.Site) => void }) {
	const [isOpen, setIsOpen] = useState(false);
	const [projectName, setProjectName] = useState('');
	const [phpVersion, setPhpVersion] = useState('8.3');
	const [dbVersion, setDbVersion] = useState('8.4');
	const [redisVersion, setRedisVersion] = useState('8.0');
	const [nodeVersion, setNodeVersion] = useState('22');
	const [composerVersion, setComposerVersion] = useState('2');

	const handleSubmit = () => {
		console.log({ projectName, phpVersion, dbVersion, redisVersion, nodeVersion, composerVersion });
		setIsOpen(false);
	};

	const handleAddSite = async () => {
		await addSite({
			id: "",
			name: projectName,
			slug: "",
			domain: "",
		});
		setIsOpen(false);
	};

	return (
		<>
			<button
				onClick={() => setIsOpen(true)}
				className="bg-blue-600 text-white px-4 py-2 rounded hover:bg-blue-700 focus:outline-none focus:ring"
			>New Site</button>

			{isOpen && (
				<div className="fixed inset-0 flex items-center justify-center bg-black bg-opacity-50 z-50">
					<div className="bg-white rounded-2xl shadow-lg w-full max-w-lg p-6">
						<h2 className="text-2xl font-semibold mb-4">Create New Site</h2>
						<div className="space-y-4">
							<div className="flex flex-col">
								<label className="mb-1 font-medium">Site Name</label>
								<input
									type="text"
									value={projectName}
									onChange={(e) => setProjectName(e.target.value)}
									className="border border-gray-300 rounded px-3 py-2 focus:outline-none focus:ring focus:border-blue-300"
									placeholder="e.g. MyAwesomeSite"
								/>
							</div>
							<div className="flex flex-col">
								<label className="mb-1 font-medium">PHP Version</label>
								<select
									value={phpVersion}
									onChange={(e) => setPhpVersion(e.target.value)}
									className="border border-gray-300 rounded px-3 py-2 focus:outline-none focus:ring focus:border-blue-300"
								>
									<option value="7.4">7.4</option>
									<option value="8.0">8.0</option>
									<option value="8.1">8.1</option>
									<option value="8.2">8.2</option>
									<option value="8.3">8.3</option>
									<option value="8.4">8.4</option>
								</select>
							</div>
							<div className="flex flex-col">
								<label className="mb-1 font-medium">Database Version</label>
								<select
									value={dbVersion}
									onChange={(e) => setDbVersion(e.target.value)}
									className="border border-gray-300 rounded px-3 py-2 focus:outline-none focus:ring focus:border-blue-300"
								>
									<option value="5.7">5.7</option>
									<option value="8.4">8.4</option>
								</select>
							</div>
							<div className="flex flex-col">
								<label className="mb-1 font-medium">Redis Version</label>
								<select
									value={redisVersion}
									onChange={(e) => setRedisVersion(e.target.value)}
									className="border border-gray-300 rounded px-3 py-2 focus:outline-none focus:ring focus:border-blue-300"
								>
									<option value="7.4">7.4</option>
									<option value="8.0">8.0</option>
								</select>
							</div>
							<div className="flex flex-col">
								<label className="mb-1 font-medium">Node / NPM Version</label>
								<select
									value={nodeVersion}
									onChange={(e) => setNodeVersion(e.target.value)}
									className="border border-gray-300 rounded px-3 py-2 focus:outline-none focus:ring focus:border-blue-300"
								>
									<option value="22">22</option>
									<option value="21">21</option>
									<option value="20">20</option>
									<option value="19">19</option>
									<option value="18">18</option>
									<option value="17">17</option>
									<option value="16">16</option>
								</select>
							</div>
							<div className="flex flex-col">
								<label className="mb-1 font-medium">Composer Version</label>
								<select
									value={composerVersion}
									onChange={(e) => setComposerVersion(e.target.value)}
									className="border border-gray-300 rounded px-3 py-2 focus:outline-none focus:ring focus:border-blue-300"
								>
									<option value="1">1.x</option>
									<option value="2">2.x</option>
								</select>
							</div>
							</div>
							<div className="mt-6 flex justify-end space-x-2">
							<button
								onClick={() => setIsOpen(false)}
								className="px-4 py-2 rounded-lg border border-gray-300 hover:bg-gray-100 focus:outline-none focus:ring"
							>Cancel</button>
							<button
								onClick={handleAddSite}
								className="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 focus:outline-none focus:ring"
							>Create</button>
						</div>
					</div>
				</div>
			)}
		</>
	);
}
