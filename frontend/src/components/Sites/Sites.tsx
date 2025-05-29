import { useState } from 'react';
import type { types } from '../../../wailsjs/go/models';
import { TrashIcon } from '@heroicons/react/24/solid';
import { NavLink } from 'react-router';

export default function Sites({ sites, deleteSite }: { sites: types.Site[], deleteSite: (id: string) => void }) {
    const [searchTerm, setSearchTerm] = useState('');

    if (sites && searchTerm !== '') {
        sites = sites.filter((site) => site?.name?.toLowerCase()?.includes(searchTerm?.toLowerCase()));
    }

    return (
        <>
            <div className="sites-search">
                <input
                    type="text"
                    placeholder="Search sites..."
                    className="w-full mb-4 p-2 border border-gray-300 text-white rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
                    onChange={(e) => setSearchTerm(e.target.value)}
                />
            </div>
            { sites && sites.length > 0 ? (
                <ul className="flex flex-col gap-4">
                    { sites.map((site) => (
                        <li key={site.id} className="flex items-center justify-between bg-white shadow-md rounded-lg p-4">
                            <NavLink to={'sites/'+site.id} end>{site.name}</NavLink>

                            <button
                                onClick={() => deleteSite(site.id)}
                                className="ml-4 p-1 rounded hover:bg-gray-100"
                                aria-label={`Delete ${site.name}`}
                            >
                                <TrashIcon className="size-5 text-gray-500" />
                            </button>
                        </li>
                    )) }
                </ul>
            ) : (
                <div className="text-gray-500 mb-4">
                    No sites found.
                </div>
            )}
            
        </>
    )
}
