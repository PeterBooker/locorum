import type { types } from '../../../wailsjs/go/models';
import { TrashIcon } from '@heroicons/react/24/solid';
import { NavLink } from 'react-router';

export default function Sites({ sites, deleteSite }: { sites: types.Site[], deleteSite: (id: string) => void }) {
    if (!sites || sites.length === 0) {
        return null;
    }

    return (
        <ul className="flex flex-col gap-4">
            { sites.map((site) => (
                <li key={site.id} className="flex items-center justify-between bg-white shadow-md rounded-lg p-4">
                    <NavLink to={'sites/'+site.id} end>{site.name}</NavLink>

                    <button
                        onClick={() => deleteSite(site.id)}
                        className="ml-4 p-1 rounded hover:bg-gray-100"
                        aria-label={`Delete ${site.name}`}
                    >
                        <TrashIcon className="h-5 w-5 text-gray-500" />
                    </button>
                </li>
            )) }
        </ul>
    )
}
